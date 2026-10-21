package fwd_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

func progressRoute(started int64, stage, state string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/snapshots/s9/progress": fwdtest.Const(200, map[string]any{"networkId": "n1", "snapshotId": "s9", "done": false, "stages": []any{
			map[string]any{"stage": "CREATION", "operationState": "SUCCEEDED", "startedAt": started, "numObjects": 12},
			map[string]any{"stage": stage, "operationState": state, "startedAt": started + 60000, "numObjects": 4500},
		}}),
		"GET /api/snapshots/s9/processEstimate": fwdtest.Const(200, map[string]any{"stageToDuration": map[string]any{"CREATION": 100000, "REACHABILITY": 116000}}),
	}
}

// A processing snapshot reads as one line: the stage computing now, its state and object count, how long it has run, Forward's own estimate, and a plain warning when it has run past the estimate.
func TestProcessingLine(t *testing.T) {
	sess, _ := fwdtest.New(t, progressRoute(time.Now().Add(-405*time.Second).UnixMilli(), "CREATION", "COMPUTING"))
	p := sess.Processing(context.Background(), "s9")
	line := p.Line()
	if p.Stage != "CREATION" || p.StageState != "COMPUTING" || p.Objects == nil || *p.Objects != 4500 || p.ElapsedSeconds == nil || *p.ElapsedSeconds < 400 || p.EstimateSeconds == nil || *p.EstimateSeconds != 216 || !p.OverEstimate() {
		t.Fatalf("%+v", p)
	}
	for _, want := range []string{"stage CREATION COMPUTING", "4500 objects", "elapsed 6m", "estimate 3m36s", "past the estimate"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}
}

// Before any stage has started (a snapshot just sent to reprocess) the line says so instead of showing nothing, and an unreadable endpoint says it could not be read.
func TestProcessingLineBeforeAnyStageAndWhenUnreadable(t *testing.T) {
	r := map[string]fwdtest.Handler{
		"GET /api/snapshots/s9/progress": fwdtest.Const(200, map[string]any{"snapshotId": "s9", "done": false, "stages": []any{map[string]any{"stage": "CREATION", "operationState": "NOT_TRIGGERED"}}}),
	}
	sess, _ := fwdtest.New(t, r)
	if l := sess.Processing(context.Background(), "s9").Line(); !strings.Contains(l, "no stage has started yet") {
		t.Errorf("%q", l)
	}
	sess2, _ := fwdtest.New(t, map[string]fwdtest.Handler{"GET /api/snapshots/s9/progress": fwdtest.Const(404, map[string]any{"message": "no"})})
	if l := sess2.Processing(context.Background(), "s9").Line(); !strings.Contains(l, "progress could not be read") {
		t.Errorf("%q", l)
	}
}

// Forward answers 409 on the progress call right after a reprocess starts (seen live); the line says progress is not available yet and does not read like a failure.
func TestProcessingLineWhenForwardAnswers409(t *testing.T) {
	sess, _ := fwdtest.New(t, map[string]fwdtest.Handler{"GET /api/snapshots/s9/progress": fwdtest.Const(409, map[string]any{"message": "Snapshot s9 cannot be used at the moment (currently PROCESSING)"})})
	l := sess.Processing(context.Background(), "s9").Line()
	if !strings.Contains(l, "not available yet") || strings.Contains(l, "could not be read") {
		t.Errorf("%q", l)
	}
}
