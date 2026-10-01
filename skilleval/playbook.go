package skilleval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PlaybookCase is a task and the playbook that should govern it. Steps are the skills the playbook has an agent call, in its order; the run
// is scored on whether the agent read the playbook and how many of those steps it took (in any order, with the in-order count beside it) (not on the answer, which needs a
// judge). Run read-only: edit skills go through the apply-refusing shim.
type PlaybookCase struct {
	Query    string   `json:"query"`
	Playbook string   `json:"playbook"`
	Steps    []string `json:"steps"`
	// Required is the playbook's stated minimum: a list of groups, each satisfied by using any one of its skills.
	Required [][]string `json:"required"`
}

// PlaybookResult is what the agent did with it.
type PlaybookResult struct {
	PlaybookCase
	Used     []string `json:"used"`
	ReadIt   bool     `json:"read_playbook"`
	Taken    int      `json:"steps_taken"`    // how many of the steps were used at all, in any order
	InOrder  int      `json:"steps_in_order"` // the longest prefix-free subsequence of Steps found in Used, in order
	Missing  []string `json:"missing_required,omitempty"`
	Coverage float64  `json:"coverage"`
	OK       bool     `json:"ok"`
	CostUSD  float64  `json:"cost_usd"`
	Error    string   `json:"error,omitempty"`
	Skipped  string   `json:"skipped,omitempty"`
}

// LoadPlaybooks reads a playbook suite.
func LoadPlaybooks(path string) ([]PlaybookCase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c []PlaybookCase
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// inOrder counts how many of steps appear in used in the same relative order (a greedy subsequence match).
func inOrder(used, steps []string) int {
	n, j := 0, 0
	for _, s := range steps {
		for k := j; k < len(used); k++ {
			if used[k] == s {
				n++
				j = k + 1 // a step that is missing does not use up the rest of the run
				break
			}
		}
	}
	return n
}

// Playbooks runs each case once and scores playbook adherence: the playbook was read, and at least 60% of its steps were taken in order.
func Playbooks(ctx context.Context, ag ExecAgent, cases []PlaybookCase, cfg Config, workRoot string) ([]PlaybookResult, error) {
	shim, _, err := ShimDir(ag.Bin)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(shim)
	ag.Bin = shim
	out := make([]PlaybookResult, len(cases))
	var mu sync.Mutex
	spent := 0.0
	sem := make(chan struct{}, max(cfg.Parallel, 1))
	var wg sync.WaitGroup
	model := "sonnet"
	if len(cfg.Models) > 0 {
		model = cfg.Models[0]
	}
	for i, c := range cases {
		i, c := i, c
		out[i] = PlaybookResult{PlaybookCase: c}
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
			dir := filepath.Join(workRoot, fmt.Sprintf("%03d-playbook", i))
			_ = os.MkdirAll(dir, 0o755)
			raw, err := ag.Session(ctx, SessionOpts{Dir: dir, Model: model, Prompt: Prompt(cfg.Network, c.Query), Skills: true, BudgetUSD: cfg.RunBudget, Timeout: 10 * time.Minute})
			if err != nil && len(raw) == 0 {
				out[i].Error = err.Error()
				return
			}
			run, _ := ParseTranscript(bytes.NewReader(raw))
			used := run.UsedSkills()
			out[i].Used, out[i].CostUSD = used, run.CostUSD
			for _, u := range used {
				out[i].ReadIt = out[i].ReadIt || u == c.Playbook
			}
			// the playbook itself is not a step; steps are the skills it names
			steps := 0
			for _, s := range c.Steps {
				_ = s
				steps++
			}
			out[i].InOrder = inOrder(used, c.Steps)
			for _, st := range c.Steps {
				for _, u := range used {
					if u == st {
						out[i].Taken++
						break
					}
				}
			}
			if steps > 0 {
				out[i].Coverage = float64(out[i].Taken) / float64(steps)
			}
			out[i].Missing = nil
			for _, g := range c.Required {
				met := false
				for _, st := range g {
					for _, u := range used {
						met = met || u == st
					}
				}
				if !met {
					out[i].Missing = append(out[i].Missing, strings.Join(g, " or "))
				}
			}
			out[i].OK = out[i].ReadIt && len(out[i].Missing) == 0
			mu.Lock()
			spent += run.CostUSD
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out, nil
}

// PlaybooksReport summarises adherence and lists each miss with what was missing.
func PlaybooksReport(rs []PlaybookResult) string {
	var b strings.Builder
	ok, ran, cost := 0, 0, 0.0
	var bad []string
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
		why := fmt.Sprintf("missing the minimum: %s; took %d of %d steps, %d in order (%s)", strings.Join(r.Missing, "; "), r.Taken, len(r.Steps), r.InOrder, strings.Join(r.Used, " > "))
		if !r.ReadIt {
			why = "did not read " + r.Playbook + "; " + why
		}
		bad = append(bad, fmt.Sprintf("- %q (%s): %s", r.Query, r.Playbook, why))
	}
	fmt.Fprintf(&b, "# Playbook adherence\n\nRead the playbook and took every step of its stated minimum (`required`: one skill from each group) in %d of %d cases (%d skipped or errored). Cost $%.2f.\n", ok, ran, len(rs)-ran, cost)
	if len(bad) > 0 {
		b.WriteString("\nMisses:\n\n" + strings.Join(bad, "\n") + "\n")
	}
	return b.String()
}
