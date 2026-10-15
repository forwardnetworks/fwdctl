package main

import (
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
)

func TestQueryIDProblemCatchesATypoLocally(t *testing.T) {
	ok := "Q_" + strings.Repeat("a", 40)
	if p := queryIDProblem(ok); p != "" {
		t.Errorf("a well-formed id was refused: %s", p)
	}
	if p := queryIDProblem("FQ_" + strings.Repeat("0", 40)); p != "" {
		t.Errorf("a library id was refused: %s", p)
	}
	if p := queryIDProblem("Q_" + strings.Repeat("a", 39)); !strings.Contains(p, "39") || !strings.Contains(p, "40") {
		t.Errorf("a short id should say how many digits it has and how many it needs: %q", p)
	}
	if p := queryIDProblem("Q_abc"); p != "" {
		t.Errorf("a placeholder that is not hex-length-checkable stays the server's call: %q", p)
	}
}

func TestACachedFailureIsNamedAsSuch(t *testing.T) {
	ms := int64(1200000)
	m := fwd.NQEMeta{Outcome: "TIMED_OUT", MillisExecuting: &ms}
	if n := cachedFailureNote(m, 3*time.Second); !strings.Contains(n, "cached failure") || !strings.Contains(n, "--fresh") {
		t.Errorf("note = %q", n)
	}
	if n := cachedFailureNote(m, 25*time.Minute); n != "" {
		t.Errorf("a failure that took its full time is not cached: %q", n)
	}
	ok := fwd.NQEMeta{Outcome: "OK", MillisExecuting: &ms}
	if n := cachedFailureNote(ok, time.Second); n != "" {
		t.Errorf("a success is not a cached failure: %q", n)
	}
}

func TestHugeInlineQueryWarningNamesTheRemedy(t *testing.T) {
	if w := inlineSizeWarning(10 * 1024); w != "" {
		t.Errorf("small: %q", w)
	}
	if w := inlineSizeWarning(300 * 1024); !strings.Contains(w, "--query-id") || !strings.Contains(w, "300 KB") {
		t.Errorf("large: %q", w)
	}
}
