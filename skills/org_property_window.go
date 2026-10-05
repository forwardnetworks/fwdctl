package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// windowPoll is how often the reprocessing is polled (nowFunc, in inspect_snapshots.go, is the clock). Both are variables so the tests can drive them.
var windowPoll = 5 * time.Second

const defaultWindowMinutes = 30

// windowPlan is what a guarded window found before it would start.
type windowPlan struct {
	blockers []string
	baseline map[string]bool // snapshot ids actively busy now
	notes    []string
	target   *forward.Snapshot
}

// orgTimeZone maps the organization's TIME_ZONE value (AMERICA_NEW_YORK) to a Go location, or nil when it cannot be mapped with certainty.
func orgTimeZone(v string) *time.Location {
	v = strings.TrimSpace(v)
	if strings.EqualFold(v, "UTC") || v == "" {
		return time.UTC
	}
	parts := strings.Split(strings.ToLower(v), "_")
	title := func(w string) string { return strings.ToUpper(w[:1]) + w[1:] }
	var rest []string
	for _, p := range parts[1:] {
		rest = append(rest, title(p))
	}
	if loc, err := time.LoadLocation(title(parts[0]) + "/" + strings.Join(rest, "_")); err == nil {
		return loc
	}
	return nil
}

// scheduleFires says whether a collection schedule may start a collection between now and end: (true, why) when it may or when that cannot be told (unknown is blocking), (false, "")
// when it certainly does not. last is the time of the network's last collection (zero when unknown); orgTZ is the organization's zone for a schedule that names none.
func scheduleFires(c forward.CollectionSchedule, now, end, last time.Time, orgTZ *time.Location) (bool, string) {
	if !c.Enabled {
		return false, ""
	}
	if c.EndAt != "" {
		if t, err := time.Parse(time.RFC3339, c.EndAt); err == nil && t.Before(now) {
			return false, ""
		}
	}
	if c.Periodic() {
		if last.IsZero() {
			return true, "a periodic schedule has no next-run time and the network's last collection time is not known"
		}
		next := last.Add(time.Duration(*c.PeriodInSeconds) * time.Second)
		if !next.After(end) {
			return true, fmt.Sprintf("a periodic schedule is due by %s (last collection %s plus %ds)", next.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339), *c.PeriodInSeconds)
		}
		return false, ""
	}
	loc := orgTZ
	if c.TimeZone != "" {
		l, err := time.LoadLocation(c.TimeZone)
		if err != nil {
			return true, "its time zone " + c.TimeZone + " could not be read"
		}
		loc = l
	}
	if loc == nil {
		return true, "it names no time zone and the organization's time zone could not be mapped"
	}
	for day := 0; day <= int(end.Sub(now).Hours()/24)+1; day++ {
		d := now.In(loc).AddDate(0, 0, day)
		if len(c.DaysOfTheWeek) > 0 {
			ok := false
			for _, w := range c.DaysOfTheWeek {
				if w == int(d.Weekday()) {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		for _, hm := range c.Times {
			var h, m int
			if _, err := fmt.Sscanf(hm, "%d:%d", &h, &m); err != nil {
				return true, "a schedule time " + hm + " could not be read"
			}
			at := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, loc)
			if !at.Before(now) && !at.After(end) {
				return true, fmt.Sprintf("a schedule fires at %s", at.UTC().Format(time.RFC3339))
			}
		}
	}
	return false, ""
}

// planWindow reads everything a guarded window depends on: the target snapshot, every snapshot being worked on in any network, and every enabled collection schedule that may fire
// before the window ends. Anything it cannot read is a blocker (unknown is never a pass).
func planWindow(ctx context.Context, s *fwd.Session, in editOrgPropertyInput, cfg fwd.OrgConfig, window time.Duration) (*windowPlan, error) {
	p := &windowPlan{baseline: map[string]bool{}}
	sn, err := s.Snapshot(ctx, in.NetworkID, in.ReprocessSnapshotID)
	switch {
	case err != nil:
		p.blockers = append(p.blockers, fmt.Sprintf("snapshot %s could not be read in network %s: %v", in.ReprocessSnapshotID, in.NetworkID, err))
	case sn == nil:
		p.blockers = append(p.blockers, fmt.Sprintf("network %s has no snapshot %s", in.NetworkID, in.ReprocessSnapshotID))
	case sn.State != "PROCESSED" || sn.Predicted():
		p.blockers = append(p.blockers, fmt.Sprintf("snapshot %s is %s (%s): only a processed collected snapshot is reprocessed in a window", in.ReprocessSnapshotID, sn.State, kindOf(*sn)))
	default:
		p.target = sn
	}
	nets, err := s.Networks(ctx)
	if err != nil {
		return nil, err
	}
	now := nowFunc()
	end := now.Add(window)
	orgTZ := orgTimeZone(cfg.Effective["time_zone"])
	for _, n := range nets {
		nid := string(n.ID)
		snaps, serr := s.Snapshots(ctx, nid)
		if serr != nil {
			p.blockers = append(p.blockers, fmt.Sprintf("network %s (%s): its snapshots could not be read, so processing there is unknown: %v", nid, n.Name, serr))
			continue
		}
		var last time.Time
		for _, sn := range snaps {
			if activelyBusy(sn.State) {
				p.baseline[string(sn.ID)] = true
				p.blockers = append(p.blockers, fmt.Sprintf("network %s (%s): snapshot %s is %s", nid, n.Name, sn.ID, sn.State))
			}
			if !sn.Predicted() && sn.ProcessingTrigger == "COLLECTION" {
				if t, perr := time.Parse(time.RFC3339, firstNonEmpty(sn.CreatedAt, sn.ProcessedAt)); perr == nil && t.After(last) {
					last = t
				}
			}
		}
		sch, _, cerr := s.Client.CollectionSchedules.List(ctx, nid)
		if cerr != nil {
			p.blockers = append(p.blockers, fmt.Sprintf("network %s (%s): its collection schedules could not be read, so a collection starting in the window cannot be ruled out: %v", nid, n.Name, cerr))
			continue
		}
		for _, c := range sch {
			if fires, why := scheduleFires(c, now, end, last, orgTZ); fires {
				p.blockers = append(p.blockers, fmt.Sprintf("network %s (%s): schedule %s: %s", nid, n.Name, c.ID, why))
			}
		}
	}
	sort.Strings(p.blockers)
	return p, nil
}

// orgPropertyWindow is a guarded window: set an org property, reprocess ONE named snapshot so the new value takes effect, wait for it to finish, and ALWAYS put the original value back
// (also when the reprocess fails or the wait runs out). It refuses to start while any snapshot in any network is being worked on or a collection schedule may fire before the window
// ends, because the property is organization-wide and every network processing in the window would use the experimental value.
func orgPropertyWindow(ctx context.Context, s *fwd.Session, in editOrgPropertyInput, w windowSpec) (result.Result, error) {
	cx := w.cx
	if in.NetworkID == "" || in.Value == "" || in.Clear {
		return result.Result{}, fmt.Errorf("%w: a window needs reprocess_snapshot_id, network_id (the snapshot's network) and value (not clear)", ErrInvalidInput)
	}
	minutes := in.WindowMinutes
	if minutes <= 0 {
		minutes = defaultWindowMinutes
	}
	if minutes > 240 {
		return result.Result{}, fmt.Errorf("%w: window_minutes is at most 240", ErrInvalidInput)
	}
	plan, err := planWindow(ctx, s, in, w.cfg, time.Duration(minutes)*time.Minute)
	if err != nil {
		return result.Result{}, err
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	steps := []string{
		fmt.Sprintf("set %s to %s (organization-wide; was %s)", strings.ToUpper(w.name), in.Value, w.before["value"]),
		fmt.Sprintf("reprocess snapshot %s of network %s and wait up to %d minutes", in.ReprocessSnapshotID, in.NetworkID, minutes),
		"put the original value back whatever happens (an override is set back to its value; no override is cleared)",
		"report any snapshot elsewhere that began processing during the window",
	}
	d := map[string]any{"property": strings.ToUpper(w.name), "window_minutes": minutes, "steps": steps, "blockers": plan.blockers, "before": w.before, "target_snapshot": in.ReprocessSnapshotID, "mode": mode}
	limits := append([]string{}, w.limits...)
	limits = append(limits, "the property is ORGANIZATION-wide: during the window every network that processes uses the new value, which is why the window refuses to start while anything is processing or a collection may start",
		"schedules show no next run: a time-of-day schedule is computed from its times, days and zone (the organization's zone when none is named); a periodic schedule is due at the network's last collection plus its period, and with no known last collection it blocks")
	ev := []result.Evidence{result.NewEvidence(result.EvState, "orgPropertyWindow", nil, d, "")}
	if len(plan.blockers) > 0 {
		shown, _ := result.CapRow(plan.blockers, 5) // the finding states the full count
		return result.Build(editOrgPropertyName, result.Failed, fmt.Sprintf("Window blocked, nothing was changed: %d blocker(s), for example: %s", len(plan.blockers), strings.Join(shown, "; ")), result.Deterministic, cx,
			result.Options{Mode: mode, Limits: limits, Evidence: ev, NextActions: []string{"inspect-snapshots"}})
	}
	ch := result.Change{Action: "org_property_window", Target: fmt.Sprintf("%s for the reprocess of snapshot %s", strings.ToUpper(w.name), in.ReprocessSnapshotID), Before: w.before,
		After: map[string]any{"during": in.Value, "afterwards": w.before}, Reversible: true, Undo: "the original value is put back as the last step of the window itself"}
	if !in.Apply {
		firstSteps, _ := result.CapRow(steps, 2)
		msg := fmt.Sprintf("Dry run: the window is clear (no snapshot in progress in any network, no collection due within %d minutes). Would %s. Nothing was changed; run again with apply=true", minutes, strings.Join(firstSteps, ", then "))
		if w.needConfirm {
			msg += fmt.Sprintf(" and confirm=%q", strings.ToUpper(w.name))
		}
		return result.Build(editOrgPropertyName, result.OK, msg, result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: ev, NextActions: []string{"edit-snapshot"}})
	}
	if w.needConfirm && !w.confirmed {
		return result.Build(editOrgPropertyName, result.Failed, fmt.Sprintf("Refused, nothing was changed: %s is classified %s; apply needs confirm=%q", strings.ToUpper(w.name), w.risk, strings.ToUpper(w.name)),
			result.Deterministic, cx, result.Options{Mode: mode, Limits: limits, Evidence: ev})
	}

	// the window: set, reprocess, wait, and restore in every case (with a context that outlives a cancelled one)
	restoreCtx := context.WithoutCancel(ctx)
	var restoreErr error
	restore := func() {
		if w.overridden {
			restoreErr = s.SetOrgProperty(restoreCtx, w.name, w.before["value"].(string))
		} else {
			restoreErr = s.ClearOrgProperty(restoreCtx, w.name)
		}
		if restoreErr == nil {
			if now, rerr := s.EffectiveOrgConfig(restoreCtx); rerr != nil {
				restoreErr = fmt.Errorf("the restore was sent but reading it back failed: %w", rerr)
			} else if !sameOrgValue(now[w.name], w.before["value"].(string)) {
				restoreErr = fmt.Errorf("the restore was sent but %s reads %q, not %q", strings.ToUpper(w.name), now[w.name], w.before["value"])
			}
		}
	}
	if err := s.SetOrgProperty(ctx, w.name, in.Value); err != nil {
		return result.Result{}, fmt.Errorf("setting the property failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	var final string
	var runErr error
	func() {
		defer restore()
		if now, rerr := s.EffectiveOrgConfig(ctx); rerr != nil || !sameOrgValue(now[w.name], in.Value) {
			runErr = fmt.Errorf("the value did not take effect (read back failed or differs); the window did not reprocess anything")
			return
		}
		if _, err := s.ReprocessSnapshot(ctx, in.ReprocessSnapshotID); err != nil {
			runErr = fmt.Errorf("reprocessing snapshot %s failed: %w", in.ReprocessSnapshotID, err)
			return
		}
		deadline := nowFunc().Add(time.Duration(minutes) * time.Minute)
		for {
			time.Sleep(windowPoll)
			sn, err := s.Snapshot(ctx, in.NetworkID, in.ReprocessSnapshotID)
			if err == nil && sn != nil {
				final = sn.State
				if !fwd.InProgress(sn.State) && sn.State != "" {
					return
				}
			}
			if ctx.Err() != nil {
				runErr = ctx.Err()
				return
			}
			if nowFunc().After(deadline) {
				runErr = fmt.Errorf("snapshot %s was still %s after %d minutes", in.ReprocessSnapshotID, orNone(final), minutes)
				return
			}
		}
	}()

	// anything elsewhere that began working during the window
	var began []string
	if after, perr := planWindowBusy(restoreCtx, s); perr == nil {
		for id, where := range after {
			if !plan.baseline[id] && id != in.ReprocessSnapshotID {
				began = append(began, where)
			}
		}
		sort.Strings(began)
	} else {
		limits = append(limits, "snapshots that began processing during the window could not be listed: "+perr.Error())
	}
	d["final_state"], d["restored"], d["began_during_window"] = final, restoreErr == nil, began
	if len(began) > 0 {
		limits = append(limits, fmt.Sprintf("%d snapshot(s) elsewhere began processing during the window and may have used the experimental value: %s", len(began), strings.Join(began, "; ")))
	}
	switch {
	case restoreErr != nil:
		return result.Build(editOrgPropertyName, result.Failed, fmt.Sprintf("RESTORE FAILED: %s may still be %s (organization-wide). Set it back to %s now. Cause: %v", strings.ToUpper(w.name), in.Value, w.before["value"], restoreErr),
			result.Deterministic, cx, result.Options{Mode: mode, Changes: []result.Change{ch}, Limits: limits, Evidence: ev})
	case runErr != nil:
		return result.Build(editOrgPropertyName, result.Failed, fmt.Sprintf("The window failed (%v); the original value %s was put back", runErr, w.before["value"]), result.Deterministic, cx,
			result.Options{Mode: mode, Changes: []result.Change{ch}, Limits: limits, Evidence: ev})
	case final != "PROCESSED":
		return result.Build(editOrgPropertyName, result.Failed, fmt.Sprintf("Snapshot %s ended %s under %s=%s; the original value %s was put back", in.ReprocessSnapshotID, final, strings.ToUpper(w.name), in.Value, w.before["value"]),
			result.Deterministic, cx, result.Options{Mode: mode, Changes: []result.Change{ch}, Limits: limits, Evidence: ev, NextActions: []string{"investigate-collection-failure"}})
	}
	return result.Build(editOrgPropertyName, result.OK, fmt.Sprintf("Snapshot %s was reprocessed under %s=%s and finished PROCESSED; the original value %s was put back (read back)", in.ReprocessSnapshotID, strings.ToUpper(w.name), in.Value, w.before["value"]),
		result.Deterministic, cx, result.Options{Mode: mode, Changes: []result.Change{ch}, Limits: limits, Evidence: ev, NextActions: []string{"inspect-snapshots", "compare-nqe-results"}})
}

// planWindowBusy lists the snapshots being worked on now, by id, with where they are.
func planWindowBusy(ctx context.Context, s *fwd.Session) (map[string]string, error) {
	nets, err := s.Networks(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, n := range nets {
		snaps, serr := s.Snapshots(ctx, string(n.ID))
		if serr != nil {
			return nil, serr
		}
		for _, sn := range snaps {
			if activelyBusy(sn.State) {
				out[string(sn.ID)] = fmt.Sprintf("network %s (%s) snapshot %s %s", n.ID, n.Name, sn.ID, sn.State)
			}
		}
	}
	return out, nil
}

// windowSpec is what editOrgProperty already worked out and hands to the window.
type windowSpec struct {
	name       string
	before     map[string]any
	overridden bool
	risk       string
	needConfirm,
	confirmed bool
	cfg    fwd.OrgConfig
	limits []string
	cx     result.Context
}
