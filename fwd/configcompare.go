package fwd

import (
	"regexp"
	"sort"
	"strings"
)

// Verdicts of CompareConfigs.
const (
	CompareSame      = "pass"            // the same lines in the same order, once device noise is dropped
	CompareOrderOnly = "pass-order-only" // the same lines in another order
	CompareDiff      = "diff"
)

// configNoise matches lines a device writes on its own and that say nothing about the configuration: commit and change timestamps, the byte count of a Cisco running config, and lone "!" separators.
var configNoise = regexp.MustCompile(`^(## Last commit|! Last configuration change|!Time:|!Command:|!Running configuration last done|Current configuration|!+$)`)

// ConfigDiff is the result of comparing a device config with the one it should be.
type ConfigDiff struct {
	Verdict string   `json:"verdict"`
	Missing []string `json:"missing,omitempty"` // lines the expected config has and the result lacks (at most 5)
	Extra   []string `json:"extra,omitempty"`   // lines the result has and the expected config lacks (at most 5)
}

func configLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, " \t\r")
		if strings.TrimSpace(l) == "" || configNoise.MatchString(l) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// SameConfig says two configs have the same lines in the same order once noise lines and trailing blanks are dropped.
func SameConfig(a, b string) bool {
	x, y := configLines(a), configLines(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// CompareConfigs compares a predicted device config (got) with the config it should be (want). It is offline text work and reads nothing from Forward.
func CompareConfigs(got, want string) ConfigDiff {
	if SameConfig(got, want) {
		return ConfigDiff{Verdict: CompareSame}
	}
	g, w := configLines(got), configLines(want)
	gs, ws := map[string]bool{}, map[string]bool{}
	for _, l := range g {
		gs[l] = true
	}
	for _, l := range w {
		ws[l] = true
	}
	var missing, extra []string
	for l := range ws {
		if !gs[l] {
			missing = append(missing, l)
		}
	}
	for l := range gs {
		if !ws[l] {
			extra = append(extra, l)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) == 0 && len(extra) == 0 {
		sg, sw := append([]string(nil), g...), append([]string(nil), w...)
		sort.Strings(sg)
		sort.Strings(sw)
		if strings.Join(sg, "\n") == strings.Join(sw, "\n") {
			return ConfigDiff{Verdict: CompareOrderOnly}
		}
	}
	if len(missing) > 5 {
		missing = missing[:5]
	}
	if len(extra) > 5 {
		extra = extra[:5]
	}
	return ConfigDiff{Verdict: CompareDiff, Missing: missing, Extra: extra}
}
