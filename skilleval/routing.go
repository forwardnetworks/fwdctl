package skilleval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/forwardnetworks/fwdctl/skills"
)

// RoutingCase is one question and the skill a model should reach for. Expect is empty when no skill should be used. Rival names
// the skill it is most likely to be confused with, so a miss shows which pair is unclear.
type RoutingCase struct {
	Query  string `json:"query"`
	Expect string `json:"expect"`
	Rival  string `json:"rival,omitempty"`
}

// RoutingResult is what the agent actually used for one case.
type RoutingResult struct {
	RoutingCase
	Used    []string `json:"used"`
	First   string   `json:"first"`
	OK      bool     `json:"ok"`
	CostUSD float64  `json:"cost_usd"`
	Error   string   `json:"error,omitempty"`
	Skipped string   `json:"skipped,omitempty"`
}

// LoadRouting reads a routing suite.
func LoadRouting(path string) ([]RoutingCase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c []RoutingCase
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// UsedSkills lists the skills a run touched in order, once each, with retired names resolved to the skill that runs now.
func (r Run) UsedSkills() []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		n, _ = skills.Resolve(n)
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, s := range r.Skills {
		add(s)
	}
	for _, c := range r.Commands {
		for _, m := range cliUse.FindAllStringSubmatch(c, -1) {
			add(m[2])
		}
	}
	return out
}

// Routing runs every case once with the skills installed and records which skill the agent reached for. There is no judge: the
// score is whether the first skill used is the expected one (or none, for an empty Expect), so it costs only the sessions.
func Routing(ctx context.Context, ag Agent, cases []RoutingCase, cfg Config, workRoot string) []RoutingResult {
	out := make([]RoutingResult, len(cases))
	var mu sync.Mutex
	spent := 0.0
	par := max(cfg.Parallel, 1)
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	model := "sonnet"
	if len(cfg.Models) > 0 {
		model = cfg.Models[0]
	}
	for i, c := range cases {
		i, c := i, c
		out[i] = RoutingResult{RoutingCase: c}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			mu.Lock()
			over := cfg.TotalBudget > 0 && spent >= cfg.TotalBudget
			mu.Unlock()
			if over || ctx.Err() != nil {
				out[i].Skipped = "total budget reached"
				return
			}
			dir := filepath.Join(workRoot, fmt.Sprintf("%03d-routing", i))
			_ = os.MkdirAll(dir, 0o755)
			raw, err := ag.Session(ctx, SessionOpts{Dir: dir, Model: model, Prompt: Prompt(cfg.Network, c.Query), Skills: true,
				BudgetUSD: cfg.RunBudget, Timeout: 6 * time.Minute})
			if err != nil && len(raw) == 0 {
				out[i].Error = err.Error()
				return
			}
			run, _ := ParseTranscript(bytes.NewReader(raw))
			used := run.UsedSkills()
			out[i].Used, out[i].CostUSD = used, run.CostUSD
			if len(used) > 0 {
				out[i].First = used[0]
			}
			want, _ := skills.Resolve(c.Expect)
			// The router tells an agent to read plan-safe-write before the first edit skill, so for an edit skill that is the right first move.
			out[i].OK = out[i].First == want || (strings.HasPrefix(want, "edit-") && out[i].First == "plan-safe-write")
			mu.Lock()
			spent += run.CostUSD
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// RoutingReport summarises accuracy and lists every miss with the pair it was between.
func RoutingReport(rs []RoutingResult) string {
	var b strings.Builder
	ok, ran, cost := 0, 0, 0.0
	type pair struct{ want, got string }
	miss := map[pair][]string{}
	for _, r := range rs {
		if r.Skipped != "" || r.Error != "" {
			continue
		}
		ran++
		cost += r.CostUSD
		if r.OK {
			ok++
			continue
		}
		p := pair{orNoneSkill(r.Expect), orNoneSkill(r.First)}
		miss[p] = append(miss[p], r.Query)
	}
	fmt.Fprintf(&b, "# Routing\n\nFirst skill was the expected one in %d of %d cases (%d skipped or errored). Cost $%.2f.\n\n", ok, ran, len(rs)-ran, cost)
	if len(miss) == 0 {
		return b.String()
	}
	pairs := make([]pair, 0, len(miss))
	for p := range miss {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool { return len(miss[pairs[i]]) > len(miss[pairs[j]]) })
	b.WriteString("| Expected | Reached for | Count | Example |\n|---|---|---|---|\n")
	for _, p := range pairs {
		fmt.Fprintf(&b, "| %s | %s | %d | %s |\n", p.want, p.got, len(miss[p]), strings.ReplaceAll(miss[p][0], "|", "/"))
	}
	return b.String()
}

func orNoneSkill(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
