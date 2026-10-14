package fwd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// BaseSnapshotID is the snapshot a change set was built on, or "" when the change set does not exist.
func (s *Session) BaseSnapshotID(ctx context.Context, networkID, changeSetID string) (string, error) {
	list, _, err := s.Client.Predict.ListChangeSets(ctx, networkID)
	if err != nil {
		return "", err
	}
	s.Annotate(intp(len(list)), false)
	for _, cs := range list {
		if string(cs.ID) == changeSetID {
			return string(cs.SnapshotID), nil
		}
	}
	return "", nil
}

// PredictedSnapshot is the newest PROCESSED predicted snapshot of a change set. Running Predict creates a
// snapshot and costs compute, so it happens only when run is true. nil means there is nothing to compare,
// which callers must report as unknown, never as "no change".
func (s *Session) PredictedSnapshot(ctx context.Context, networkID, changeSetID string, run bool) (*forward.Snapshot, error) {
	list, _, err := s.Client.Predict.ListPredictedSnapshots(ctx, networkID, changeSetID)
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(list)), false)
	var ready []forward.PredictedSnapshot
	for _, p := range list {
		if p.State == stateProcessed {
			ready = append(ready, p)
		}
	}
	if len(ready) > 0 {
		sort.SliceStable(ready, func(i, j int) bool { return ready[i].ProcessedAt > ready[j].ProcessedAt })
		return s.Snapshot(ctx, networkID, string(ready[0].ID))
	}
	if !run {
		return nil, nil
	}
	poller, _, err := s.Client.Predict.RunOperation(ctx, networkID, changeSetID, "fwdctl verify")
	if err != nil {
		return nil, err
	}
	done, _, err := poller.Wait(ctx, forward.PollOptions[forward.Snapshot]{})
	if err != nil {
		return nil, err
	}
	return done, nil
}

// Connectivity is the subnet-connectivity comparison once Forward has finished computing it.
type Connectivity struct {
	// Settled is false when the computation did not finish in time; its zeros mean "not finished".
	Settled bool
	// Compared is false when zero subnet pairs were evaluated: "nothing changed" and "nothing was compared"
	// look identical in the counts, and only TotalPairs tells them apart.
	Compared       bool
	NewlyIsolated  int64
	NewlyConnected int64
	Modified       int64
	TotalPairs     int64
	// Unavailable carries Forward's reason when the comparison is switched off for this organization (403).
	// It is not a failure of the skill and not a zero: the connectivity impact was simply not measured.
	Unavailable string
}

// SubnetConnectivity waits for the comparison to settle, up to timeout.
func (s *Session) SubnetConnectivity(ctx context.Context, beforeID, afterID string, timeout time.Duration) (Connectivity, error) {
	d, _, err := s.Client.Diffs.WaitForSubnetConnectivity(ctx, beforeID, afterID, forward.ConnectivityWaitOptions{
		Timeout: timeout, PollInterval: pollInterval(timeout)})
	if errors.Is(err, forward.ErrConnectivityDiffPartial) {
		return Connectivity{}, nil
	}
	// Forward gates some routes on an organization property and answers 403 "<PROPERTY> is off for your
	// organization". The SDK classifies that exact message; any other 403 is a real permission problem.
	var er *forward.ErrorResponse
	if errors.Is(err, forward.ErrFeatureGated) && errors.As(err, &er) {
		return Connectivity{Unavailable: er.Message}, nil
	}
	if err != nil {
		return Connectivity{}, err
	}
	return Connectivity{Settled: true, Compared: d.TotalSubnetPairs > 0, NewlyIsolated: d.NewlyIsolatedSubnetPairs,
		NewlyConnected: d.NewlyConnectedSubnetPairs, Modified: d.ModifiedSubnetPairs, TotalPairs: d.TotalSubnetPairs}, nil
}

// pollInterval keeps short timeouts (tests, quick checks) from waiting a fixed two seconds per poll.
func pollInterval(timeout time.Duration) time.Duration {
	if timeout > 0 && timeout < 10*time.Second {
		return timeout / 4
	}
	return 0
}

// AreaCount is one diff family's count.
type AreaCount struct {
	Area     string
	Count    int64
	Complete bool
	// Unavailable is true when Forward would not answer for this family; it is not a count of zero.
	Unavailable bool
}

// AreaCounts reads every countable diff family. One request per family.
func (s *Session) AreaCounts(ctx context.Context, beforeID, afterID string) ([]AreaCount, error) {
	var out []AreaCount
	for _, kind := range forward.CountableDiffKinds {
		c, _, err := s.Client.Diffs.Count(ctx, beforeID, afterID, kind)
		if err != nil {
			var er *forward.ErrorResponse
			if errors.As(err, &er) && er.Response != nil && er.Response.StatusCode >= 400 && er.Response.StatusCode < 500 {
				out = append(out, AreaCount{Area: string(kind), Unavailable: true})
				continue
			}
			return nil, err
		}
		out = append(out, AreaCount{Area: string(kind), Count: c.Count, Complete: c.Complete})
	}
	s.Annotate(intp(len(out)), false)
	return out, nil
}

// CheckState is one check's outcome on one snapshot.
type CheckState struct {
	ID         string
	Name       string
	Status     string
	Violations int64
	Enabled    bool
}

// Checks lists a snapshot's checks. Disabled checks are returned marked, not dropped.
func (s *Session) Checks(ctx context.Context, snapshotID string) ([]CheckState, error) {
	list, _, err := s.Client.Checks.List(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(list)), false)
	out := make([]CheckState, 0, len(list))
	for _, c := range list {
		cs := CheckState{ID: string(c.ID), Name: c.Name, Status: c.Status, Enabled: c.Enabled == nil || *c.Enabled}
		if c.NumViolations != nil {
			cs.Violations = *c.NumViolations
		}
		out = append(out, cs)
	}
	return out, nil
}

// IsBad reports a failing status: FAIL, ERROR or TIMEOUT.
func IsBad(status string) bool { return status == "FAIL" || status == "ERROR" || status == "TIMEOUT" }

// Regression is a check that got worse between two snapshots.
type Regression struct {
	CheckID          string `json:"check_id"`
	Name             string `json:"name"`
	StatusBefore     string `json:"status_before"`
	StatusAfter      string `json:"status_after"`
	ViolationsBefore *int64 `json:"violations_before"`
	ViolationsAfter  int64  `json:"violations_after"`
}

// CheckRegressions lists checks with more violations, or that went from passing to FAIL/ERROR/TIMEOUT.
// A check already failing before and unchanged is not a regression.
func (s *Session) CheckRegressions(ctx context.Context, beforeID, afterID string) ([]Regression, error) {
	before, err := s.Checks(ctx, beforeID)
	if err != nil {
		return nil, err
	}
	after, err := s.Checks(ctx, afterID)
	if err != nil {
		return nil, err
	}
	prev := map[string]CheckState{}
	for _, c := range before {
		prev[c.ID] = c
	}
	var out []Regression
	for _, c := range after {
		if !c.Enabled {
			continue
		}
		b, had := prev[c.ID]
		wasBad := had && IsBad(b.Status)
		more := had && c.Violations > b.Violations
		if (IsBad(c.Status) && !wasBad) || more {
			r := Regression{CheckID: c.ID, Name: c.Name, StatusAfter: c.Status, ViolationsAfter: c.Violations}
			if had {
				r.StatusBefore = b.Status
				v := b.Violations
				r.ViolationsBefore = &v
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// ChangeSet finds one change set by id. nil means the network has no such change set.
func (s *Session) ChangeSet(ctx context.Context, networkID, changeSetID string) (*forward.ChangeSet, error) {
	list, _, err := s.Client.Predict.ListChangeSets(ctx, networkID)
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(list)), false)
	for i := range list {
		if string(list[i].ID) == changeSetID {
			return &list[i], nil
		}
	}
	return nil, nil
}

// PredictedSnapshots lists a change set's predictions (running none).
func (s *Session) PredictedSnapshots(ctx context.Context, networkID, changeSetID string) ([]forward.PredictedSnapshot, error) {
	list, _, err := s.Client.Predict.ListPredictedSnapshots(ctx, networkID, changeSetID)
	if err == nil {
		s.Annotate(intp(len(list)), false)
	}
	return list, err
}

// ChangeSetChecks lists the change set's checks with their results on one snapshot (its base, or one of its predictions).
func (s *Session) ChangeSetChecks(ctx context.Context, networkID, changeSetID, snapshotID string) ([]forward.ChangeSetCheckResult, error) {
	list, _, err := s.Client.Predict.ListChangeSetChecks(ctx, networkID, changeSetID, snapshotID)
	if err == nil {
		s.Annotate(intp(len(list)), false)
	}
	return list, err
}

// SecurityRulesDiff reads a device's rulebases marked with how the change set alters them.
func (s *Session) SecurityRulesDiff(ctx context.Context, networkID, changeSetID, device string) (*forward.SecurityRuleTableDiff, error) {
	d, _, err := s.Client.Predict.SecurityRulesDiff(ctx, networkID, changeSetID, device)
	if err == nil && d != nil {
		s.Annotate(intp(len(d.Entries)), false)
	}
	return d, err
}

// CreateChangeSet makes an empty draft change set on a base snapshot.
func (s *Session) CreateChangeSet(ctx context.Context, networkID, name, description, snapshotID string) (*forward.ChangeSet, error) {
	cs, _, err := s.Client.Predict.CreateChangeSet(ctx, networkID, forward.ChangeSetCreateRequest{Name: name, Description: description, SnapshotID: snapshotID})
	return cs, err
}

// DeleteChangeSet removes a draft change set. Predicted snapshots it already made stay.
func (s *Session) DeleteChangeSet(ctx context.Context, networkID, changeSetID string) error {
	_, err := s.Client.Predict.DeleteChangeSet(ctx, networkID, changeSetID)
	return err
}

// ValidateCommands asks Forward whether CLI text parses for a device, without staging it. An empty error list is not proof the
// commands are right: the validator only knows the commands in its grammar.
func (s *Session) ValidateCommands(ctx context.Context, networkID, changeSetID, device, commands string) ([]forward.CommandError, error) {
	v, _, err := s.Client.Predict.ValidateCommands(ctx, networkID, changeSetID, device, commands)
	if err != nil || v == nil {
		return nil, err
	}
	return v.CommandErrors, nil
}

// StageCommands records CLI text on a device of the draft. It reaches no device.
func (s *Session) StageCommands(ctx context.Context, networkID, changeSetID, device, commands string) error {
	_, err := s.Client.Predict.StageCommands(ctx, networkID, changeSetID, device, commands)
	return err
}

// StageBGPAdvertisements records external BGP advertisements on a device of the draft.
func (s *Session) StageBGPAdvertisements(ctx context.Context, networkID, changeSetID, device string, ads []forward.BGPAdvertisement) error {
	_, err := s.Client.Predict.StageBGPAdvertisements(ctx, networkID, changeSetID, device, ads)
	return err
}

// ChangeSets lists the network's change sets.
func (s *Session) ChangeSets(ctx context.Context, networkID string) ([]forward.ChangeSet, error) {
	list, _, err := s.Client.Predict.ListChangeSets(ctx, networkID)
	if err == nil {
		s.Annotate(intp(len(list)), false)
	}
	return list, err
}

// ACLDeviceChange is one device whose ACLs changed between two snapshots, with the count of rules added, deleted and modified.
type ACLDeviceChange struct {
	Device                   string
	Added, Deleted, Modified int
}

// ACLChangedDevices lists the devices whose ACLs changed, the most changed first. It carries counts, not rules: ACLRuleChanges lists one device's rules.
func (s *Session) ACLChangedDevices(ctx context.Context, beforeID, afterID string) ([]ACLDeviceChange, error) {
	list, _, err := s.Client.Diffs.ACLDevices(ctx, beforeID, afterID)
	if err != nil {
		return nil, err
	}
	out := make([]ACLDeviceChange, 0, len(list))
	for _, d := range list {
		out = append(out, ACLDeviceChange{Device: d.DeviceName, Added: d.Stats["ADDED"], Deleted: d.Stats["DELETED"], Modified: d.Stats["MODIFIED"]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].Added+out[i].Deleted+out[i].Modified, out[j].Added+out[j].Deleted+out[j].Modified
		if ti != tj {
			return ti > tj
		}
		return out[i].Device < out[j].Device
	})
	s.Annotate(intp(len(out)), false)
	return out, nil
}

// ACLRule is one side of a changed ACL rule in a stable shape: its action, name, where it applies, its match conditions as "FIELD=values" text and the config lines it
// came from ("file": "10-12, 20-20").
type ACLRule struct {
	Name, Action, Context string
	InputInterfaces       []string
	OutputInterfaces      []string
	Matches               []string
	FileLines             map[string]string
}

// ACLRuleChange is one rule that was added (After only), deleted (Before only) or modified (both).
type ACLRuleChange struct {
	Change        string // ADDED | DELETED | MODIFIED
	Before, After *ACLRule
}

// ACLRuleChanges lists one device's ACL rules that changed between two snapshots, in the order Forward returns them, with Forward's counts by kind of change.
func (s *Session) ACLRuleChanges(ctx context.Context, beforeID, afterID, device string) ([]ACLRuleChange, map[string]int, error) {
	d, _, err := s.Client.Diffs.DeviceACL(ctx, beforeID, afterID, device)
	if err != nil {
		return nil, nil, err
	}
	rule := func(e *forward.ACLDiffEntry) *ACLRule {
		if e == nil {
			return nil
		}
		r := &ACLRule{Name: e.Name, Action: e.Action, Context: e.ACLContext, InputInterfaces: e.InputInterfaces, OutputInterfaces: e.OutputInterfaces}
		for _, m := range e.Matches {
			r.Matches = append(r.Matches, matchText(m, false))
		}
		for _, m := range e.NegatedMatches {
			r.Matches = append(r.Matches, matchText(m, true))
		}
		if len(e.FileLines) > 0 {
			r.FileLines = map[string]string{}
			for file, ranges := range e.FileLines {
				parts := make([]string, 0, len(ranges))
				for _, x := range ranges {
					if x.Start == x.End {
						parts = append(parts, fmt.Sprint(x.Start))
					} else {
						parts = append(parts, fmt.Sprintf("%d-%d", x.Start, x.End))
					}
				}
				r.FileLines[file] = strings.Join(parts, ", ")
			}
		}
		return r
	}
	out := make([]ACLRuleChange, 0, len(d.Diffs))
	for _, e := range d.Diffs {
		out = append(out, ACLRuleChange{Change: e.Type, Before: rule(e.Before), After: rule(e.After)})
	}
	s.Annotate(intp(len(out)), false)
	return out, d.Stats, nil
}

// matchText renders one ACL match condition as FIELD=values (or FIELD!=values when negated). Values arrive raw because their shape follows the field.
func matchText(m forward.ACLMatch, negated bool) string {
	op := "="
	if negated || m.Negated {
		op = "!="
	}
	vals := strings.TrimSpace(string(m.Values))
	if vals == "" || vals == "null" {
		return m.Field
	}
	return m.Field + op + vals
}
