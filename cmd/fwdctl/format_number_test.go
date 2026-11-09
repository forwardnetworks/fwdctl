package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A whole number beyond 2^53 (kept exact by fwd.Records) prints exactly in a table or CSV cell, and the NQE compare calls it a number.
func TestCellPrintsALargeWholeNumberExactly(t *testing.T) {
	if got := cell(json.Number("9007199254740993")); got != "9007199254740993" {
		t.Fatalf("cell = %q", got)
	}
	if got := cell(float64(42)); got != "42" {
		t.Fatalf("cell = %q", got)
	}
	if got := jsonType(json.Number("18446744073709551615")); got != "number" {
		t.Fatalf("jsonType = %q", got)
	}
}

// A skill result printed as a table or CSV keeps a whole number beyond 2^53 exact: the rows are found by re-reading the result, which must not
// turn the number back into a float64.
func TestSkillRowsAsCSVKeepALargeWholeNumberExact(t *testing.T) {
	res := map[string]any{"evidence": []any{map[string]any{"detail": map[string]any{"rows": []any{
		map[string]any{"name": "a", "octets": json.Number("9007199254740993")},
	}}}}}
	rows, _ := largestRows(res)
	var b strings.Builder
	if err := renderRows(&b, rows, "csv"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "9007199254740993") {
		t.Fatalf("csv rounded the number: %s", b.String())
	}
	found := false
	for _, l := range allLists(res) {
		if l.key == "rows" {
			found = cell(l.rows[0]["octets"]) == "9007199254740993"
		}
	}
	if !found {
		t.Fatalf("allLists rounded or lost the number: %+v", allLists(res))
	}
}

// A query that found no rows prints an empty JSON array, not null, so a consumer that expects a list can tell "read, nothing there" from a bad read.
func TestNoRowsPrintAsAnEmptyJSONArray(t *testing.T) {
	for _, rows := range [][]map[string]any{nil, {}} {
		var b strings.Builder
		if err := renderRows(&b, rows, "json"); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(b.String()); got != "[]" {
			t.Fatalf("json for no rows = %q, want []", got)
		}
	}
}
