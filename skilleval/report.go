package skilleval

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type tally struct {
	runs, triggerOK, triggerN         int
	behMet, behN                      int
	answered, grounded, outN, groundN int
	cost                              float64
	tools, turns                      int
	judgeErr, errs                    int
}

func (t *tally) add(r CaseResult) {
	if r.Skipped != "" {
		return
	}
	t.runs++
	t.cost += r.Run.CostUSD
	t.tools += r.Run.ToolCalls
	t.turns += r.Run.Turns
	if r.Run.Error != "" {
		t.errs++
	}
	if r.Arm == "with" {
		t.triggerN++
		if r.TriggerOK {
			t.triggerOK++
		}
	}
	for _, v := range r.Verdicts {
		t.behN++
		if v.Met {
			t.behMet++
		}
	}
	if r.Outcome != nil {
		t.outN++
		if r.Outcome.Answered {
			t.answered++
		}
		if !r.Outcome.GroundedNA {
			t.groundN++
			if r.Outcome.Grounded {
				t.grounded++
			}
		}
	}
	if r.JudgeError != "" {
		t.judgeErr++
	}
}

func pct(a, b int) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d (%d%%)", a, b, 100*a/b)
}

func (t tally) avgCost() string {
	if t.runs == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.3f", t.cost/float64(t.runs))
}

func (t tally) avgTools() string {
	if t.runs == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", float64(t.tools)/float64(t.runs))
}

// Report renders the results as markdown: totals per model and arm, per skill, and every miss with the judge's reason.
func Report(results []CaseResult, cfg Config, started time.Time) string {
	var b strings.Builder
	total := 0.0
	skipped := 0
	for _, r := range results {
		total += r.Run.CostUSD
		if r.Skipped != "" {
			skipped++
		}
	}
	fmt.Fprintf(&b, "# Skill evaluation\n\nRun %s. Network %s. Models: %s. Judge: %s. %d runs, $%.2f spent",
		started.Format("2006-01-02 15:04"), cfg.Network, strings.Join(cfg.Models, ", "), cfg.JudgeModel, len(results)-skipped, total)
	if skipped > 0 {
		fmt.Fprintf(&b, ", **%d not run (budget)**", skipped)
	}
	b.WriteString(".\n\n")

	byKey := map[string]*tally{}
	get := func(k string) *tally {
		if byKey[k] == nil {
			byKey[k] = &tally{}
		}
		return byKey[k]
	}
	var models, skills []string
	seenM, seenS := map[string]bool{}, map[string]bool{}
	for _, r := range results {
		get(r.Model + "|" + r.Arm).add(r)
		get("skill|" + r.Skill + "|" + r.Model + "|" + r.Arm).add(r)
		if !seenM[r.Model] {
			seenM[r.Model] = true
			models = append(models, r.Model)
		}
		if !seenS[r.Skill] {
			seenS[r.Skill] = true
			skills = append(skills, r.Skill)
		}
	}
	sort.Strings(skills)

	b.WriteString("## With and without the skills\n\n| Model | Arm | Triggered as expected | Expected behaviours met | Answered | Grounded | Avg cost | Avg tool calls |\n|---|---|---|---|---|---|---|---|\n")
	for _, m := range models {
		for _, arm := range cfg.Arms {
			t := byKey[m+"|"+arm]
			if t == nil {
				continue
			}
			trig, beh := "-", "-"
			if arm == "with" {
				trig, beh = pct(t.triggerOK, t.triggerN), pct(t.behMet, t.behN)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", m, arm, trig, beh, pct(t.answered, t.outN), pct(t.grounded, t.groundN), t.avgCost(), t.avgTools())
		}
	}

	b.WriteString("\n## By skill (with the skills)\n\n| Skill | Model | Triggered as expected | Behaviours met | Grounded | Avg cost |\n|---|---|---|---|---|---|\n")
	for _, s := range skills {
		for _, m := range models {
			t := byKey["skill|"+s+"|"+m+"|with"]
			if t == nil {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", s, m, pct(t.triggerOK, t.triggerN), pct(t.behMet, t.behN), pct(t.grounded, t.groundN), t.avgCost())
		}
	}

	var misses []string
	for _, r := range results {
		if r.Skipped != "" {
			continue
		}
		where := fmt.Sprintf("%s / %s / %s / case %d", r.Skill, r.Model, r.Arm, r.Index+1)
		if r.Run.Error != "" {
			misses = append(misses, fmt.Sprintf("- **%s** run error: %s", where, r.Run.Error))
		}
		if r.Arm == "with" && !r.TriggerOK && r.Run.Error == "" {
			want := "should have triggered"
			if !r.ShouldTrigger {
				want = "should NOT have triggered"
			}
			misses = append(misses, fmt.Sprintf("- **%s** %s: %q", where, want, r.Query))
		}
		for _, v := range r.Verdicts {
			if !v.Met {
				misses = append(misses, fmt.Sprintf("- **%s** not met: %s (%s)", where, v.Behavior, v.Why))
			}
		}
		if r.Outcome != nil && (!r.Outcome.Answered || (!r.Outcome.Grounded && !r.Outcome.GroundedNA)) {
			misses = append(misses, fmt.Sprintf("- **%s** answered=%v grounded=%v: %s", where, r.Outcome.Answered, r.Outcome.Grounded, r.Outcome.Why))
		}
		if r.JudgeError != "" {
			misses = append(misses, fmt.Sprintf("- **%s** judge error: %s", where, r.JudgeError))
		}
	}
	b.WriteString("\n## Misses\n\n")
	if len(misses) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString(strings.Join(misses, "\n") + "\n")
	}
	return b.String()
}
