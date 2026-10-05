package skilleval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Case is one evaluation query.
type Case struct {
	Query            string   `json:"query"`
	ShouldTrigger    *bool    `json:"should_trigger"`
	ExpectedBehavior []string `json:"expected_behavior"`
	Note             string   `json:"note"`
}

// Suite is the evaluation of one skill.
type Suite struct {
	Skill string `json:"skill"`
	// ProcedureOnly marks a skill with no runner (it only teaches a procedure), so its runs have no tool output to
	// ground an answer in. Grounding is not scored for them.
	ProcedureOnly bool   `json:"procedure_only,omitempty"`
	Cases         []Case `json:"cases"`
}

// LoadSuites reads evals/skills/*.json; only, if not empty, limits it to those skills.
func LoadSuites(dir string, only []string) ([]Suite, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	want := map[string]bool{}
	for _, o := range only {
		want[o] = true
	}
	var out []Suite
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var s Suite
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if len(want) == 0 || want[s.Skill] {
			out = append(out, s)
		}
	}
	return out, nil
}

// Agent runs one headless session and returns its stream-json transcript. It is an interface so the scoring can be
// tested without a model.
type Agent interface {
	Session(ctx context.Context, o SessionOpts) ([]byte, error)
	// Ask runs a tool-less one-shot prompt and returns the model's reply text (used for judging).
	Ask(ctx context.Context, model, prompt string) (string, error)
}

// SessionOpts describes one agent session.
type SessionOpts struct {
	Dir    string
	Model  string
	Prompt string
	Skills bool // install the forward skills for this session
	// APISpec, when set, makes this the raw-API arm: no skills and no fwdctl on PATH, only Forward's API spec (copied into the
	// session directory) and the credentials in the environment, so the model must call the API itself.
	APISpec   string
	BudgetUSD float64
	Timeout   time.Duration
}

// Config controls an evaluation run.
type Config struct {
	Models      []string
	Arms        []string // "with", "without", "api" (needs APISpec)
	APISpec     string   // path to Forward's OpenAPI spec, for the "api" arm
	SkillsDir   string   // directory of skills/<name>/SKILL.md, shown to the outcome judge for the skills a run loaded
	Network     string
	MaxCases    int // per skill; 0 means all
	Parallel    int
	JudgeModel  string
	RunBudget   float64
	TotalBudget float64
}

// CaseResult is one (case, model, arm) outcome.
type CaseResult struct {
	Skill         string    `json:"skill"`
	Model         string    `json:"model"`
	Arm           string    `json:"arm"`
	Index         int       `json:"index"`
	Query         string    `json:"query"`
	ShouldTrigger bool      `json:"should_trigger"`
	Triggered     bool      `json:"triggered"`
	TriggerOK     bool      `json:"trigger_ok"`
	Run           Run       `json:"run"`
	Verdicts      []Verdict `json:"verdicts,omitempty"`
	Outcome       *Outcome  `json:"outcome,omitempty"`
	JudgeError    string    `json:"judge_error,omitempty"`
	Skipped       string    `json:"skipped,omitempty"`
}

// Prompt is what the agent is asked: the eval query plus the context a real user of that network would have.
func Prompt(network, query string) string {
	return fmt.Sprintf("Our Forward network id is %s. Forward credentials are already configured in the environment. %s", network, query)
}

// APIPrompt is the prompt of the raw-API arm: the same question, with the spec and the way to call the API instead of fwdctl.
func APIPrompt(network, query string) string {
	return fmt.Sprintf("Our Forward network id is %s. Forward's API specification is the file api-spec.yaml in the current directory (search it, do not read it whole). "+
		"Call the API with curl using HTTP basic auth: the base URL is $FORWARD_URL and the credentials are $FORWARD_USERNAME and $FORWARD_PASSWORD. %s", network, query)
}

type job struct {
	suite Suite
	i     int
	model string
	arm   string
}

// Evaluate evaluates every suite. Results come back in a stable order. Spending stops when TotalBudget is reached; the
// jobs not started are returned as Skipped so a partial run says so.
func Evaluate(ctx context.Context, ag Agent, suites []Suite, cfg Config, workRoot string) []CaseResult {
	var jobs []job
	for _, s := range suites {
		for _, i := range SelectCases(s.Cases, cfg.MaxCases) {
			for _, m := range cfg.Models {
				for _, arm := range cfg.Arms {
					jobs = append(jobs, job{s, i, m, arm})
				}
			}
		}
	}
	results := make([]CaseResult, len(jobs))
	var mu sync.Mutex
	spent := 0.0
	par := cfg.Parallel
	if par < 1 {
		par = 1
	}
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for n, j := range jobs {
		n, j := n, j
		c := j.suite.Cases[j.i]
		res := CaseResult{Skill: j.suite.Skill, Model: j.model, Arm: j.arm, Index: j.i, Query: c.Query, ShouldTrigger: c.ShouldTrigger != nil && *c.ShouldTrigger}
		mu.Lock()
		over := cfg.TotalBudget > 0 && spent >= cfg.TotalBudget
		mu.Unlock()
		if over || ctx.Err() != nil {
			res.Skipped = "total budget reached"
			results[n] = res
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			mu.Lock()
			over := cfg.TotalBudget > 0 && spent >= cfg.TotalBudget
			mu.Unlock()
			if over {
				res.Skipped = "total budget reached"
				results[n] = res
				return
			}
			dir := filepath.Join(workRoot, fmt.Sprintf("%03d-%s-%s-%s", n, j.suite.Skill, j.model, j.arm))
			_ = os.MkdirAll(dir, 0o755)
			out := runOne(ctx, ag, res, c, j, cfg, dir)
			mu.Lock()
			spent += out.Run.CostUSD
			mu.Unlock()
			results[n] = out
		}()
	}
	wg.Wait()
	return results
}

func runOne(ctx context.Context, ag Agent, res CaseResult, c Case, j job, cfg Config, dir string) CaseResult {
	prompt, spec := Prompt(cfg.Network, c.Query), ""
	if j.arm == "api" {
		prompt, spec = APIPrompt(cfg.Network, c.Query), cfg.APISpec
	}
	raw, err := ag.Session(ctx, SessionOpts{Dir: dir, Model: j.model, Prompt: prompt, APISpec: spec,
		Skills: j.arm == "with", BudgetUSD: cfg.RunBudget, Timeout: 6 * time.Minute})
	if err != nil && len(raw) == 0 {
		res.Run.Error = err.Error()
		return res
	}
	run, perr := ParseTranscript(bytes.NewReader(raw))
	if perr != nil && run.Final == "" {
		res.Run.Error = perr.Error()
		return res
	}
	res.Run = run
	res.Triggered = run.Triggered(j.suite.Skill)
	if j.arm == "with" {
		res.TriggerOK = res.Triggered == res.ShouldTrigger
	}
	// Judge only runs that should have used the skill: a run that must NOT trigger is scored on triggering alone.
	if !res.ShouldTrigger || run.Final == "" {
		return res
	}
	if j.arm == "with" && len(c.ExpectedBehavior) > 0 {
		reply, err := ag.Ask(ctx, cfg.JudgeModel, JudgePrompt(Prompt(cfg.Network, c.Query), c.ExpectedBehavior, run))
		if err == nil {
			res.Verdicts, err = ParseVerdicts(reply, c.ExpectedBehavior)
		}
		if err != nil {
			res.JudgeError = err.Error()
		}
	}
	if reply, err := ag.Ask(ctx, cfg.JudgeModel, OutcomePromptWithSkills(Prompt(cfg.Network, c.Query), run, LoadedSkillText(cfg.SkillsDir, run))); err == nil {
		if o, err := ParseOutcome(reply); err == nil {
			o.GroundedNA = j.suite.ProcedureOnly
			res.Outcome = &o
		} else if res.JudgeError == "" {
			res.JudgeError = err.Error()
		}
	} else if res.JudgeError == "" {
		res.JudgeError = err.Error()
	}
	return res
}

// stripNUL removes NUL bytes. Tool output can carry them, and an argument containing one makes exec fail with
// "invalid argument", which lost two judge verdicts in a pilot.
func stripNUL(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// ExecAgent drives the claude CLI.
type ExecAgent struct {
	Claude string // path to claude
	Bin    string // directory holding the fwdctl binary
	Env    []string
}

func (a ExecAgent) base() string {
	if a.Claude != "" {
		return a.Claude
	}
	return "claude"
}

func (a ExecAgent) env() []string { return a.envFor(true) }

// envFor builds the environment; the raw-API arm leaves fwdctl off PATH.
func (a ExecAgent) envFor(withBin bool) []string {
	env := append(os.Environ(), a.Env...)
	if a.Bin != "" && withBin {
		env = append(env, "PATH="+a.Bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

func (a ExecAgent) Session(ctx context.Context, o SessionOpts) ([]byte, error) {
	if o.APISpec != "" {
		b, err := os.ReadFile(o.APISpec)
		if err != nil {
			return nil, fmt.Errorf("api spec: %v", err)
		}
		if err := os.WriteFile(filepath.Join(o.Dir, "api-spec.yaml"), b, 0o644); err != nil {
			return nil, err
		}
	}
	if o.Skills {
		install := exec.CommandContext(ctx, filepath.Join(a.Bin, "fwdctl"), "install", "claude", "--dir", filepath.Join(o.Dir, ".claude", "skills"))
		install.Env = a.env()
		if out, err := install.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("install skills: %v: %s", err, out)
		}
	}
	tools := "Bash,Read"
	if o.Skills {
		tools += ",Skill"
	}
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	args := []string{"-p", o.Prompt, "--model", o.Model, "--output-format", "stream-json", "--verbose",
		"--setting-sources", "project", "--permission-mode", "bypassPermissions", "--tools", tools,
		"--no-session-persistence", "--max-budget-usd", fmt.Sprintf("%.2f", o.BudgetUSD)}
	cmd := exec.CommandContext(ctx, a.base(), args...)
	cmd.Dir = o.Dir
	cmd.Env = a.envFor(o.APISpec == "")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}

func (a ExecAgent) Ask(ctx context.Context, model, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "skill-eval-judge-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	// A NUL byte in a tool result (binary output) makes exec fail with "invalid argument"; it is never meaningful here.
	prompt = stripNUL(prompt)
	cmd := exec.CommandContext(ctx, a.base(), "-p", prompt, "--model", model, "--output-format", "json",
		"--setting-sources", "project", "--tools", "", "--no-session-persistence")
	cmd.Dir = dir
	cmd.Env = a.env()
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	var doc struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		return "", fmt.Errorf("judge output: %w", err)
	}
	return strings.TrimSpace(doc.Result), nil
}

// SelectCases picks which cases to run. With a limit it keeps the first triggering cases and always one that must not
// trigger the skill, so a small pilot still tests for false triggers.
func SelectCases(cases []Case, max int) []int {
	if max <= 0 || max >= len(cases) {
		idx := make([]int, len(cases))
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	neg := -1
	for i, c := range cases {
		if c.ShouldTrigger != nil && !*c.ShouldTrigger {
			neg = i
			break
		}
	}
	var idx []int
	for i, c := range cases {
		if len(idx) == max-boolToInt(neg >= 0) {
			break
		}
		if i != neg && c.ShouldTrigger != nil && *c.ShouldTrigger {
			idx = append(idx, i)
		}
	}
	if neg >= 0 {
		idx = append(idx, neg)
	}
	return idx
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// LoadedSkillText reads the SKILL.md of each skill the run loaded, from dir (empty dir: none).
func LoadedSkillText(dir string, run Run) map[string]string {
	if dir == "" {
		return nil
	}
	out := map[string]string{}
	for _, n := range run.Skills {
		if b, err := os.ReadFile(filepath.Join(dir, n, "SKILL.md")); err == nil {
			out[n] = string(b)
		}
	}
	return out
}

// Rejudge re-grades the outcome of the with-skills results that have one, showing the judge the skills each run loaded. It runs
// no agent sessions; only the judge model is called. It returns how many outcomes were re-graded and how many changed.
func Rejudge(ctx context.Context, ag Agent, results []CaseResult, cfg Config) (regraded, changed int) {
	for i := range results {
		r := &results[i]
		if r.Arm != "with" || r.Outcome == nil || r.Run.Final == "" {
			continue
		}
		reply, err := ag.Ask(ctx, cfg.JudgeModel, OutcomePromptWithSkills(Prompt(cfg.Network, r.Query), r.Run, LoadedSkillText(cfg.SkillsDir, r.Run)))
		if err != nil {
			continue
		}
		o, err := ParseOutcome(reply)
		if err != nil {
			continue
		}
		o.GroundedNA = r.Outcome.GroundedNA
		regraded++
		if o.Grounded != r.Outcome.Grounded || o.Answered != r.Outcome.Answered {
			changed++
		}
		r.Outcome = &o
	}
	return regraded, changed
}
