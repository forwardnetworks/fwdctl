package main

import (
	"bytes"
	"context"
	"errors"
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
