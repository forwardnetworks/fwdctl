package result

import (
	"encoding/json"
	"strings"
	"testing"
)

func snap(s string) *string { return &s }

var ctx = Context{NetworkID: "n1", SnapshotID: snap("s1"), State: "current"}

func goodFailed() Result {
	return MustBuild("investigate-reachability", Failed, "Traffic is denied by security policy", Deterministic, ctx, Options{
		Evidence:    []Evidence{NewEvidence(EvPath, "getPaths", snap("s1"), map[string]any{"device": "fw01"}, "")},
		NextActions: []string{"verify-change"},
	})
}

func TestAGoodResultValidatesAndRoundTrips(t *testing.T) {
	r := goodFailed()
	if p := r.Validate(); len(p) != 0 {
		t.Fatalf("valid result reported problems: %v", p)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"evidence":null`) || strings.Contains(string(b), `"limits":null`) {
		t.Fatalf("empty collections must marshal as [], got %s", b)
	}
}

// POSITIVE CONTROL: the validator must be able to say no, or a green means nothing.
func TestMalformedResultsAreRefused(t *testing.T) {
	cases := map[string]func(*Result){
		"missing schema":              func(r *Result) { r.Schema = "" },
		"bad status":                  func(r *Result) { r.Status = "maybe" },
		"failed with no evidence":     func(r *Result) { r.Evidence = []Evidence{} },
		"failed but unknown conf":     func(r *Result) { r.Confidence = NoBasis },
		"empty finding":               func(r *Result) { r.Finding = " " },
		"bad skill name":              func(r *Result) { r.Skill = "Bad Name" },
		"evidence without provenance": func(r *Result) { r.Evidence[0].Source.Operation = "" },
		"unknown evidence kind":       func(r *Result) { r.Evidence[0].Type = "vibes" },
		"nil evidence detail":         func(r *Result) { r.Evidence[0].Detail = nil },
		"null limits":                 func(r *Result) { r.Limits = nil },
		"no network id":               func(r *Result) { r.Context.NetworkID = "" },
		"bad next action":             func(r *Result) { r.NextActions = []string{"Not A Skill"} },
	}
	for name, mutate := range cases {
		r := goodFailed()
		mutate(&r)
		if len(r.Validate()) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUnknownNeedsALimitAndUnknownConfidence(t *testing.T) {
	if _, err := NewUnknown("check-network-compliance", "zero rows", ctx, nil, Options{}); err == nil {
		t.Error("unknown with no limit was accepted")
	}
	r, err := NewUnknown("check-network-compliance", "zero rows", ctx, []string{"matched 0 devices; nothing was checked"}, Options{})
	if err != nil || r.Status != Unknown || r.Confidence != NoBasis {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestBuildRefusesOKWithoutEvidence(t *testing.T) {
	if _, err := Build("verify-change", OK, "verified", Deterministic, ctx, Options{}); err == nil {
		t.Error("ok without evidence was accepted")
	}
}

func TestWriteResultsMustSayTheirModeAndHowToUndo(t *testing.T) {
	ok := func(mut func(*Result)) []string {
		r := MustBuild("edit-snapshot", OK, "planned", Deterministic, Context{NetworkID: "n"}, Options{
			Evidence: []Evidence{NewEvidence(EvState, "op", nil, nil, "")}, Mode: ModeDryRun,
			Changes: []Change{{Action: "set_note", Target: "snapshot 1", Reversible: true, Undo: "set the note back"}}})
		mut(&r)
		return r.Validate()
	}
	if p := ok(func(*Result) {}); len(p) != 0 {
		t.Fatalf("a valid plan was refused: %v", p)
	}
	for name, mut := range map[string]func(*Result){
		"changes without a mode":           func(r *Result) { r.Mode = "" },
		"a dry run reporting applied":      func(r *Result) { r.Changes[0].Applied = true },
		"applied listing an unapplied one": func(r *Result) { r.Mode = ModeApplied },
		"reversible with no undo":          func(r *Result) { r.Changes[0].Undo = "" },
		"a change with no target":          func(r *Result) { r.Changes[0].Target = "" },
		"an unknown mode":                  func(r *Result) { r.Mode = "maybe" },
	} {
		if p := ok(mut); len(p) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestOmissionsAreRenderedIntoLimitsAndValidated(t *testing.T) {
	rows, om := Cap([]int{1, 2, 3, 4, 5}, 2, "rows", "narrow with device")
	if len(rows) != 2 || len(om) != 1 || om[0].Total != 5 {
		t.Fatalf("cap: %v %v", rows, om)
	}
	if rows, om := Cap([]int{1, 2}, 2, "rows", ""); len(rows) != 2 || om != nil {
		t.Fatalf("a list that fits is not an omission: %v %v", rows, om)
	}
	page, om2, ok := Page([]int{1, 2, 3, 4, 5}, 2, 2, "rows")
	if !ok || len(page) != 2 || page[0] != 3 || om2[0].From != 2 || !strings.Contains(om2[0].Text(), "offset=4") {
		t.Fatalf("page: %v %v %v", page, om2, ok)
	}
	r, err := Build("x-skill", OK, "found", Deterministic, Context{NetworkID: "n"}, Options{Evidence: []Evidence{NewEvidence(EvState, "op", nil, map[string]any{}, "")}, Omitted: om})
	if err != nil || len(r.Omitted) != 1 || !strings.Contains(strings.Join(r.Limits, " "), "5 rows in all; 2 shown") {
		t.Fatalf("build: %v %v", err, r.Limits)
	}
	if _, err := Build("x-skill", OK, "found", Deterministic, Context{NetworkID: "n"}, Options{Evidence: []Evidence{NewEvidence(EvState, "op", nil, map[string]any{}, "")}, Omitted: []Omission{{What: "rows", Total: 2, Shown: 2}}}); err == nil {
		t.Errorf("an omission that omits nothing is invalid")
	}
}
