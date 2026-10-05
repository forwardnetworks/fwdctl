// Command skill-eval runs the evaluations in evals/skills against a real agent (the claude CLI, headless) with and
// without the skills installed, judges the runs, and prints a markdown report.
//
//	skill-eval --bin DIR [--models sonnet] [--arms with,without] [--skills a,b] [--max-cases N]
//	           [--network ID] [--parallel 4] [--budget 10] [--out DIR]
//
// DIR holds the fwdctl binary. FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD must be set in the environment.
// Every run spends real model tokens: --budget is the total dollar cap and --run-budget the cap per session.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/skilleval"
)

func main() {
	var (
		evals     = flag.String("evals", "evals/skills", "directory of per-skill evaluation files")
		bin       = flag.String("bin", "", "directory containing the fwdctl binary (required)")
		models    = flag.String("models", "sonnet", "comma-separated models to run")
		arms      = flag.String("arms", "with,without", "with the skills, without, api (raw API spec, no skills, no fwdctl; needs --api-spec), or a list")
		skillsDir = flag.String("skills-dir", "skills", "directory of skills/<name>/SKILL.md, shown to the grounding judge for the skills a run loaded")
		rejudge   = flag.String("rejudge", "", "re-judge the outcome (answered, grounded) of the with-skills runs in this results.json, with the skill text shown to the judge; runs no agent sessions")
		apiSpec   = flag.String("api-spec", "", "path to Forward's OpenAPI spec, for the api arm")
		only      = flag.String("skills", "", "comma-separated skills to evaluate (default all)")
		max       = flag.Int("max-cases", 0, "cases per skill (0 = all)")
		network   = flag.String("network", "", "Forward network id the queries run against (required)")
		par       = flag.Int("parallel", 4, "sessions to run at once")
		judge     = flag.String("judge", "sonnet", "model that judges the runs")
		runB      = flag.Float64("run-budget", 1.0, "dollar cap per session")
		totalB    = flag.Float64("budget", 10.0, "dollar cap for the whole run; remaining cases are skipped")
		playbooks = flag.String("playbooks", "", "run the playbook-adherence suite (evals/playbooks.json): did the agent read the playbook and take its steps in order; applies are refused by a shim")
		protocol  = flag.String("protocol", "", "run the write-protocol suite (evals/protocol.json): the agent must plan, never apply, and ask for approval; applies are refused by a shim")
		routing   = flag.String("routing", "", "run a routing suite (evals/routing.json): which skill the agent reaches for, no judging")
		out       = flag.String("out", "", "directory for transcripts, results.json and report.md (default: a temp directory)")
	)
	flag.Parse()
	if *bin == "" || *network == "" {
		fmt.Fprintln(os.Stderr, "error: --bin and --network are required")
		os.Exit(64)
	}
	for _, v := range []string{"FORWARD_URL", "FORWARD_USERNAME", "FORWARD_PASSWORD"} {
		if os.Getenv(v) == "" {
			fmt.Fprintf(os.Stderr, "error: %s is not set\n", v)
			os.Exit(64)
		}
	}
	split := func(s string) []string {
		var o []string
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				o = append(o, p)
			}
		}
		return o
	}
	if *playbooks != "" {
		cases, err := skilleval.LoadPlaybooks(*playbooks)
		if err != nil || len(cases) == 0 {
			fmt.Fprintf(os.Stderr, "error: no playbook cases in %s: %v\n", *playbooks, err)
			os.Exit(64)
		}
		dir := *out
		if dir == "" {
			dir, _ = os.MkdirTemp("", "skill-playbooks-")
		}
		abs, _ := filepath.Abs(*bin)
		res, err := skilleval.Playbooks(context.Background(), skilleval.ExecAgent{Bin: abs}, cases, skilleval.Config{Models: split(*models), Network: *network,
			Parallel: *par, RunBudget: *runB, TotalBudget: *totalB}, filepath.Join(dir, "work"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(res, "", " ")
		_ = os.WriteFile(filepath.Join(dir, "playbooks.json"), b, 0o644)
		fmt.Println(skilleval.PlaybooksReport(res))
		return
	}
	if *protocol != "" {
		cases, err := skilleval.LoadProtocol(*protocol)
		if err != nil || len(cases) == 0 {
			fmt.Fprintf(os.Stderr, "error: no protocol cases in %s: %v\n", *protocol, err)
			os.Exit(64)
		}
		dir := *out
		if dir == "" {
			dir, _ = os.MkdirTemp("", "skill-protocol-")
		}
		abs, _ := filepath.Abs(*bin)
		res, err := skilleval.Protocol(context.Background(), skilleval.ExecAgent{Bin: abs}, cases, skilleval.Config{Models: split(*models), Network: *network,
			Parallel: *par, RunBudget: *runB, TotalBudget: *totalB}, filepath.Join(dir, "work"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(res, "", " ")
		_ = os.WriteFile(filepath.Join(dir, "protocol.json"), b, 0o644)
		fmt.Println(skilleval.ProtocolReport(res))
		return
	}
	if *routing != "" {
		cases, err := skilleval.LoadRouting(*routing)
		if err != nil || len(cases) == 0 {
			fmt.Fprintf(os.Stderr, "error: no routing cases in %s: %v\n", *routing, err)
			os.Exit(64)
		}
		dir := *out
		if dir == "" {
			dir, _ = os.MkdirTemp("", "skill-routing-")
		}
		abs, _ := filepath.Abs(*bin)
		res := skilleval.Routing(context.Background(), skilleval.ExecAgent{Bin: abs}, cases, skilleval.Config{Models: split(*models), Network: *network,
			Parallel: *par, RunBudget: *runB, TotalBudget: *totalB}, filepath.Join(dir, "work"))
		b, _ := json.MarshalIndent(res, "", " ")
		_ = os.WriteFile(filepath.Join(dir, "routing.json"), b, 0o644)
		fmt.Println(skilleval.RoutingReport(res))
		return
	}
	if *rejudge != "" {
		b, err := os.ReadFile(*rejudge)
		var rs []skilleval.CaseResult
		if err == nil {
			err = json.Unmarshal(b, &rs)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(64)
		}
		abs, _ := filepath.Abs(*bin)
		cfg := skilleval.Config{Models: split(*models), Arms: split(*arms), Network: *network, JudgeModel: *judge, SkillsDir: *skillsDir}
		n, ch := skilleval.Rejudge(context.Background(), skilleval.ExecAgent{Bin: abs}, rs, cfg)
		fmt.Fprintf(os.Stderr, "re-graded %d outcomes, %d changed\n", n, ch)
		out2, _ := json.MarshalIndent(rs, "", " ")
		_ = os.WriteFile(*rejudge+".rejudged.json", out2, 0o644)
		fmt.Println(skilleval.Report(rs, cfg, time.Now()))
		return
	}
	suites, err := skilleval.LoadSuites(*evals, split(*only))
	if err != nil || len(suites) == 0 {
		fmt.Fprintf(os.Stderr, "error: no evaluations loaded from %s: %v\n", *evals, err)
		os.Exit(64)
	}
	dir := *out
	if dir == "" {
		dir, _ = os.MkdirTemp("", "skill-eval-")
	}
	_ = os.MkdirAll(dir, 0o755)
	abs, _ := filepath.Abs(*bin)
	cfg := skilleval.Config{Models: split(*models), Arms: split(*arms), Network: *network, MaxCases: *max, Parallel: *par,
		JudgeModel: *judge, RunBudget: *runB, TotalBudget: *totalB, APISpec: *apiSpec, SkillsDir: *skillsDir}
	started := time.Now()
	results := skilleval.Evaluate(context.Background(), skilleval.ExecAgent{Bin: abs}, suites, cfg, filepath.Join(dir, "work"))
	b, _ := json.MarshalIndent(results, "", " ")
	_ = os.WriteFile(filepath.Join(dir, "results.json"), b, 0o644)
	report := skilleval.Report(results, cfg, started)
	_ = os.WriteFile(filepath.Join(dir, "report.md"), []byte(report), 0o644)
	fmt.Println(report)
	fmt.Fprintf(os.Stderr, "results in %s\n", dir)
}
