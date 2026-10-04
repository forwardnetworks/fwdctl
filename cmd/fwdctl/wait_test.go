package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/forwardnetworks/fwdctl/fwdtest"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// fakeClock advances only when the wait sleeps, so a two-hour wait runs instantly.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time        { return c.t }
func (c *fakeClock) sleep(d time.Duration) { c.t = c.t.Add(d) }

func runWait(t *testing.T, seq []any, want, adv string, timeout time.Duration) (int, string, string) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	i := 0
	get := func(context.Context) (*forward.Snapshot, error) {
		v := seq[min(i, len(seq)-1)]
		i++
		if err, ok := v.(error); ok {
			return nil, err
		}
		return v.(*forward.Snapshot), nil
	}
	var out, errb bytes.Buffer
	code := waitForSnapshot(context.Background(), get, "s1", want, adv, timeout, time.Minute, clk.sleep, clk.now, &out, &errb)
	return code, out.String(), errb.String()
}

func snap(state, adv string) *forward.Snapshot {
	return &forward.Snapshot{State: state, AdvancedReachabilityState: adv}
}

func TestWaitReachesProcessedThenAdvancedReachability(t *testing.T) {
	code, out, errs := runWait(t, []any{snap("PROCESSING", "UNPROCESSED"), snap("PROCESSED", "UNPROCESSED"), snap("PROCESSED", "PROCESSING"), snap("PROCESSED", "PROCESSED")}, "PROCESSED", "PROCESSED", time.Hour)
	if code != waitReached || !strings.Contains(out, `"status":"ok"`) || !strings.Contains(out, `"waited_seconds":180`) || strings.Count(errs, "snapshot s1") != 4 {
		t.Fatalf("code %d out %s err %s", code, out, errs)
	}
}

func TestWaitStopsOnAFinalFailureAndOnTimeout(t *testing.T) {
	if code, out, _ := runWait(t, []any{snap("PROCESSING", ""), snap("FAILED", "")}, "PROCESSED", "", time.Hour); code != waitFailed || !strings.Contains(out, `"status":"failed"`) {
		t.Errorf("a failed snapshot ends the wait: %d %s", code, out)
	}
	if code, _, errs := runWait(t, []any{snap("PROCESSED", "PROCESSING"), snap("PROCESSED", "TIMED_OUT")}, "PROCESSED", "PROCESSED", time.Hour); code != waitFailed || !strings.Contains(errs, "advanced reachability") {
		t.Errorf("a final advanced state ends the wait: %d %s", code, errs)
	}
	if code, out, _ := runWait(t, []any{snap("PROCESSING", "")}, "PROCESSED", "", 5*time.Minute); code != waitTimedOut || !strings.Contains(out, `"status":"timed_out"`) {
		t.Errorf("still processing at the timeout: %d %s", code, out)
	}
	// without --advanced-reachability a failed advanced state does not matter
	if code, _, _ := runWait(t, []any{snap("PROCESSED", "FAILED")}, "PROCESSED", "", time.Hour); code != waitReached {
		t.Errorf("not asked for advanced reachability: %d", code)
	}
}

func TestWaitToleratesOneFailedPollButNotFive(t *testing.T) {
	boom := errors.New("connection reset")
	if code, _, _ := runWait(t, []any{boom, snap("PROCESSED", "")}, "PROCESSED", "", time.Hour); code != waitReached {
		t.Errorf("one failed poll is retried: %d", code)
	}
	if code, _, errs := runWait(t, []any{boom}, "PROCESSED", "", time.Hour); code != 3 || !strings.Contains(errs, "5 polls in a row") {
		t.Errorf("five failed polls end it: %d %s", code, errs)
	}
}

func TestWaitSaysWhenNothingIsProducingTheStateItWaitsFor(t *testing.T) {
	code, _, errs := runWait(t, []any{snap("PROCESSED", "UNPROCESSED")}, "PROCESSED", "PROCESSED", 10*time.Minute)
	if code != waitTimedOut || strings.Count(errs, "edit-advanced-reachability") != 1 {
		t.Errorf("one hint, then the timeout: %d %s", code, errs)
	}
	if _, _, errs := runWait(t, []any{snap("UNPROCESSED", "")}, "PROCESSED", "", 10*time.Minute); !strings.Contains(errs, "edit-snapshot-reprocess") {
		t.Errorf("an UNPROCESSED snapshot is called out: %s", errs)
	}
}

func TestEstimateRowsBytesCatchesAHugeResultFromASample(t *testing.T) {
	row := map[string]any{"device": strings.Repeat("x", 100)}
	rows := make([]map[string]any, 1_000_000)
	for i := range rows {
		rows[i] = row
	}
	if est := estimateRowsBytes(rows); est < maxOutputBytes {
		t.Errorf("a million 100-byte rows is over the limit: %d", est)
	}
	if estimateRowsBytes(nil) != 0 || estimateRowsBytes(rows[:10]) > 10_000 {
		t.Errorf("small results are small")
	}
}

func TestRunWithAFwdctlCommandOrATypoAnswersAtOnceWithoutReadingStdin(t *testing.T) {
	for _, name := range []string{"list", "nosuchskill"} {
		var out, errb bytes.Buffer
		blocked := &blockingReader{}
		a := &app{in: blocked, out: &out, err: &errb}
		a.newRoot()
		if code := a.runSkill(name, "", "json", "", false); code != usage || blocked.read {
			t.Errorf("%s: code %d, stdin read %v, %s", name, code, blocked.read, errb.String())
		}
		if name == "list" && !strings.Contains(errb.String(), "fwdctl command, not a name to run") {
			t.Errorf("%s", errb.String())
		}
	}
}

// blockingReader fails the test if anything reads it: a run with a bad name must not wait for stdin.
type blockingReader struct{ read bool }

func (b *blockingReader) Read([]byte) (int, error) { b.read = true; return 0, errors.New("read") }

func TestRunWithNoNameListsWhatCanFollowAndATypoSuggestsNames(t *testing.T) {
	var out, errb bytes.Buffer
	a := &app{in: strings.NewReader(""), out: &out, err: &errb}
	a.newRoot()
	if code := a.runMenu(); code != 0 {
		t.Fatalf("asking what can follow is not an error: %d", code)
	}
	for _, want := range []string{"inspect-networks", "fwdctl run NAME --help", "fwdctl which", "fwdctl help --tree", "edit-"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the menu lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "plan-investigation") {
		t.Errorf("a playbook is read, not run, so it is not in the run menu")
	}
	if got := nearNames("inspect-netwrks", 3); len(got) == 0 || got[0] != "inspect-networks" {
		t.Errorf("a typo finds its name: %v", got)
	}
}

func TestNqeRunRetriesAGatewayErrorOnlyWhenAskedAndRecordsTheAttempts(t *testing.T) {
	transientSleep = func(time.Duration) {}
	t.Cleanup(func() { transientSleep = time.Sleep })
	calls := 0
	r := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"POST /api/nqe": func(*http.Request, []byte) (int, any) {
			calls++
			if calls == 1 {
				return 502, map[string]any{"message": "bad gateway"}
			}
			return 200, map[string]any{"items": []any{map[string]any{"x": 1}}, "totalNumItems": 1}
		},
	}
	meta := t.TempDir() + "/m.json"
	// not asked: the 502 is the answer
	if code, _, _ := call(t, []string{"nqe", "run", "--network", "n1", "--format", "json"}, "select {x: 1}", r); code == 0 || calls != 1 {
		t.Fatalf("without --retry-transient a 502 fails: code %d calls %d", code, calls)
	}
	calls = 0
	code, out, errb := call(t, []string{"nqe", "run", "--network", "n1", "--format", "json", "--retry-transient", "2", "--meta", meta}, "select {x: 1}", r)
	if code != 0 || calls != 2 || !strings.Contains(out, `"x": 1`) || !strings.Contains(errb, "retrying in 5s") {
		t.Fatalf("code %d calls %d out %s err %s", code, calls, out, errb)
	}
	b, _ := os.ReadFile(meta)
	if !strings.Contains(string(b), `"attempts": 2`) || !strings.Contains(string(b), `"transport_clean": false`) {
		t.Errorf("the meta records the attempts and that the run was not transport-clean: %s", b)
	}
}
