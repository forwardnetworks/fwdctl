package fwd

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Processing is where a snapshot's processing stands, read from Forward's progress and processing-time estimate: the stage computing now, how long
// processing has run, and Forward's own estimate of the total. A field is nil when Forward gave no answer for it; Err says a call failed. It is what
// `fwdctl wait snapshot` prints on each poll and what verify-change shows for a prediction that has not finished.
type Processing struct {
	Stage           string // the stage computing now (CREATION, TEXT_SEARCH_INDEX, REACHABILITY, ADVANCED_REACHABILITY); empty when none is
	StageState      string
	Objects         *int64 // objects the current stage has reported
	ElapsedSeconds  *int64 // since the earliest stage started (a reprocess keeps the snapshot's original createdAt, so this is not since createdAt)
	EstimateSeconds *int64 // the sum of Forward's per-stage durations from earlier runs of this network: an estimate, not a promise
	Done            bool
	NoStageStarted  bool // no stage has a start time yet
	Err             error
}

// ProcessingClock is the clock for elapsed time (a test replaces it).
var ProcessingClock = time.Now

// Processing reads a snapshot's processing progress. It never fails: a call that errors is in Err and the fields it would have filled stay nil.
func (s *Session) Processing(ctx context.Context, snapshotID string) Processing {
	var p Processing
	prog, err := s.SnapshotProgress(ctx, snapshotID)
	if err != nil || prog == nil {
		if err == nil {
			err = fmt.Errorf("no answer")
		}
		p.Err = err
		return p
	}
	p.Done = prog.Done
	var earliest time.Time
	for _, st := range prog.Stages {
		if t, ok := st.StartedAt(); ok && (earliest.IsZero() || t.Before(earliest)) {
			earliest = t
		}
		if st.OperationState == "COMPUTING" && p.Stage == "" {
			p.Stage, p.StageState, p.Objects = st.Stage, st.OperationState, st.NumObjects
		}
	}
	if earliest.IsZero() {
		p.NoStageStarted = true
	} else {
		e := max(int64(ProcessingClock().Sub(earliest).Seconds()), 0)
		p.ElapsedSeconds = &e
	}
	if est, err := s.SnapshotProcessEstimate(ctx, snapshotID); err == nil && est != nil && len(est.StageToDurationMillis) > 0 {
		var total int64
		for _, ms := range est.StageToDurationMillis {
			total += ms
		}
		total /= 1000
		p.EstimateSeconds = &total
	}
	return p
}

// OverEstimate says processing has run longer than Forward's estimate. A stuck prediction looks like this: the stage stays COMPUTING while elapsed passes the estimate.
func (p Processing) OverEstimate() bool {
	return p.ElapsedSeconds != nil && p.EstimateSeconds != nil && *p.ElapsedSeconds > *p.EstimateSeconds
}

func dur(sec int64) string { return (time.Duration(sec) * time.Second).String() }

// Line is the one-line form: "stage CREATION COMPUTING, 4500 objects, elapsed 6m45s, estimate 3m36s (3m9s past the estimate)".
func (p Processing) Line() string {
	switch {
	case p.Err != nil && Status(p.Err) == 409:
		return "progress not available yet (Forward answers 409 while a snapshot is starting to process)"
	case p.Err != nil:
		return "processing progress could not be read (" + p.Err.Error() + ")"
	case p.NoStageStarted:
		return "no stage has started yet (Forward has not begun processing it)"
	}
	var parts []string
	if p.Stage != "" {
		parts = append(parts, "stage "+p.Stage+" "+p.StageState)
	} else if p.Done {
		parts = append(parts, "all stages finished")
	} else {
		parts = append(parts, "no stage computing right now")
	}
	if p.Objects != nil {
		parts = append(parts, fmt.Sprintf("%d objects", *p.Objects))
	}
	if p.ElapsedSeconds != nil {
		parts = append(parts, "elapsed "+dur(*p.ElapsedSeconds))
	}
	if p.EstimateSeconds != nil {
		parts = append(parts, "estimate "+dur(*p.EstimateSeconds))
	}
	line := strings.Join(parts, ", ")
	if p.OverEstimate() {
		line += fmt.Sprintf(" (%s past the estimate)", dur(*p.ElapsedSeconds-*p.EstimateSeconds))
	}
	return line
}
