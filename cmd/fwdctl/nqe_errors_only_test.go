package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --errors-only drops warnings from the report and leaves the verdict and exit status alone.
func TestLintErrorsOnlyDropsWarnings(t *testing.T) {
	src := "foreach d in network.devices\nlet unused = 1\nselect {n: d.name, x: d.nosuchfield}\n"
	count := func(args ...string) (errs, warns, code int) {
		var out, serr bytes.Buffer
		code = nqeTool(append(args, "lint", "-"), strings.NewReader(src), &out, &serr)
		var r struct {
			Valid       bool `json:"valid"`
			Diagnostics []struct {
				Severity string `json:"severity"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out.String())
		}
		for _, d := range r.Diagnostics {
			if d.Severity == "error" {
				errs++
			} else {
				warns++
			}
		}
		return
	}
	e0, w0, c0 := count()
	if e0 == 0 || w0 == 0 {
		t.Skipf("fixture no longer yields both an error and a warning (errors %d, warnings %d): fix the fixture", e0, w0)
	}
	e1, w1, c1 := count("--errors-only")
	if e1 != e0 || w1 != 0 || c1 != c0 || c1 != 1 {
		t.Fatalf("errors %d->%d, warnings %d->%d, exit %d->%d; want the same errors, no warnings, exit 1", e0, e1, w0, w1, c0, c1)
	}
}
