package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// ServiceNow incidents of a snapshot's checks: check-network-compliance view incidents (read) and edit-checks actions raise_incident,
// link_incident, unlink_incident, close_incident and refresh_incidents (writes). Preview routes, read from Forward's source by the SDK
// session (v0.8.18), not yet run against a live ServiceNow.

const checkIncidentsLimit = "ServiceNow check incidents: Preview routes, read from Forward's source, not yet run against a live ServiceNow. Listing needs VIEW_CHECK_INTEGRATIONS and VIEW_CHECKS, changing needs OPERATE_CHECK_INTEGRATIONS; " +
	"every call goes through the deployment's outbound connections to the org's ServiceNow (inspect-platform area integrations). Forward sends only each link's check id, number, state and when the state last changed"

// allCheckTypes are the check types the incident list is asked for, spelled as Forward parses them.
var allCheckTypes = []string{forward.CheckTypeIsolation, forward.CheckTypeReachability, forward.CheckTypeExists, forward.CheckTypeQueryString, forward.CheckTypePredefined, forward.CheckTypeNetworkQuery}

const maxIncidentReads = 10

func msTime(ms int64) any {
	if ms <= 0 {
		return nil
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func incidentLinkRow(l forward.ServiceNowIncidentLink) map[string]any {
	return map[string]any{"check_id": string(l.CheckID), "number": l.Number, "state": firstNonEmpty(l.State, "(not reported)"), "state_last_updated": msTime(l.StateLastUpdated)}
}

// checkIncidents is check-network-compliance view incidents: the incidents linked to the snapshot's active checks; with check_id, that
// check's incidents read from ServiceNow (URL, impact, urgency) with Forward's update history.
func checkIncidents(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectChecksInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Catalogue || len(in.Statuses) > 0 {
		return result.Result{}, fmt.Errorf("%w: view incidents takes snapshot_id, check_id, limit and offset (statuses and catalogue belong to view read)", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(inspectChecksName, "The network has no processed snapshot to read check incidents from", cx,
			[]string{"no processed snapshot: incidents are listed per snapshot's checks"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	sid := string(sn.ID)
	limits := []string{checkIncidentsLimit}
	if cx.State == "predicted" {
		limits = append(limits, "this is a predicted snapshot: Forward serves check incidents on one only when the org property PREDICT_UNSANDBOXED is on, so treat a refusal or an empty list here as unsupported, not as none")
	}
	if _, configured, _, e := s.Client.Integrations.GetServiceNow(ctx); e == nil && !configured {
		return result.NewUnknown(inspectChecksName, "This organization has no ServiceNow integration, so no check has an incident", cx,
			append(limits, "set one up with edit-platform area integrations name servicenow"), result.Options{NextActions: []string{"inspect-platform", "edit-platform"}})
	}
	links, _, err := s.Client.Integrations.ListCheckIncidents(ctx, sid, allCheckTypes)
	if err != nil {
		if r, ok := denialResult(inspectChecksName, err, cx, "listing the checks' ServiceNow incidents"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	rows := make([]map[string]any, 0, len(links))
	for _, l := range links {
		if in.CheckID != "" && string(l.CheckID) != in.CheckID {
			continue
		}
		rows = append(rows, incidentLinkRow(l))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return fmt.Sprint(rows[i]["check_id"], rows[i]["number"]) < fmt.Sprint(rows[j]["check_id"], rows[j]["number"])
	})
	scope := fmt.Sprintf("the active checks of snapshot %s", sid)
	if in.CheckID != "" {
		if _, e := s.CheckDetail(ctx, sid, in.CheckID); fwd.NotFound(e) {
			return result.NewUnknown(inspectChecksName, fmt.Sprintf("Snapshot %s has no check %s", sid, in.CheckID), cx,
				[]string{"no such check on this snapshot (ids are matched exactly); view read lists the checks"}, result.Options{NextActions: []string{"check-network-compliance"}})
		} else if e != nil {
			limits = append(limits, "whether check "+in.CheckID+" exists could not be read ("+e.Error()+")")
		}
		scope = fmt.Sprintf("check %s on snapshot %s", in.CheckID, sid)
	}
	if len(rows) == 0 {
		return result.Build(inspectChecksName, result.OK, fmt.Sprintf("No ServiceNow incident is linked to %s", scope), result.Deterministic, cx,
			result.Options{Limits: append(limits, "only incidents Forward has linked to a check are listed; an incident raised in ServiceNow by hand and never linked is not seen here"),
				NextActions: []string{"edit-checks"}, Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "listCheckIncidents", cx.SnapshotID, map[string]any{"incidents": rows, "total": 0}, "")}})
	}
	win, omitted, ok := window(rows, in.Limit, in.Offset, 50, 200, "incidents")
	if !ok {
		return result.NewUnknown(inspectChecksName, fmt.Sprintf("Offset %d is beyond the %d incidents", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	if in.CheckID != "" {
		failed := 0
		for i, row := range win {
			if i >= maxIncidentReads {
				row["read"] = false
				continue
			}
			n := fmt.Sprint(row["number"])
			if inc, _, e := s.Client.Integrations.GetServiceNowIncident(ctx, n); e == nil && inc != nil {
				row["sys_id"], row["summary"], row["impact"], row["urgency"], row["url"] = inc.SysID, inc.Summary, inc.Impact, inc.Urgency, inc.URL
				if inc.State != "" {
					row["state_in_servicenow"] = inc.State
				}
			} else if e != nil {
				failed++
				row["servicenow_error"] = e.Error()
			}
			if h, _, e := s.Client.Integrations.CheckIncidentUpdateHistory(ctx, sid, in.CheckID, n); e == nil {
				ups := make([]map[string]any, 0, len(h))
				for _, u := range h {
					ups = append(ups, map[string]any{"snapshot_created": msTime(u.SnapshotCreationTime), "total_failures": u.TotalFailures})
				}
				row["updates"] = ups
			} else {
				row["updates_error"] = e.Error()
			}
		}
		if failed > 0 {
			limits = append(limits, fmt.Sprintf("%d incident(s) could not be read from ServiceNow (each row says why); the link Forward holds is still listed", failed))
		}
		if len(win) > maxIncidentReads {
			limits = append(limits, fmt.Sprintf("only the first %d incidents of this page were read from ServiceNow; page with offset for the rest", maxIncidentReads))
		}
	} else {
		limits = append(limits, "pass check_id to read one check's incidents from ServiceNow (URL, impact, urgency) with Forward's update history")
	}
	open := 0
	for _, r := range rows {
		if st := strings.ToLower(fmt.Sprint(r["state"])); st != "closed" && st != "resolved" && st != "canceled" {
			open++
		}
	}
	finding := fmt.Sprintf("%d ServiceNow incident(s) linked to %s, %d not resolved, closed or canceled", len(rows), scope, open)
	return result.Build(inspectChecksName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"edit-checks", "check-network-compliance"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "listCheckIncidents", cx.SnapshotID, map[string]any{"incidents": win, "total": len(rows), "offset": in.Offset}, finding)}})
}

// incidentLevels maps the levels edit-checks takes to ServiceNow's numbers.
var incidentLevels = map[string]string{"HIGH": forward.ServiceNowIncidentLevelHigh, "MEDIUM": forward.ServiceNowIncidentLevelMedium, "LOW": forward.ServiceNowIncidentLevelLow,
	"1": forward.ServiceNowIncidentLevelHigh, "2": forward.ServiceNowIncidentLevelMedium, "3": forward.ServiceNowIncidentLevelLow}

type checkIncidentInput struct {
	Number  string `json:"number"`
	Summary string `json:"summary"`
	Details string `json:"details"`
	Impact  string `json:"impact"`
	Urgency string `json:"urgency"`
}

func validateIncidentAction(in manageChecksInput) error {
	inc := in.Incident
	if in.Action != "refresh_incidents" && in.Action != "close_incident" && in.CheckID == "" {
		return fmt.Errorf("%w: %s needs check_id", ErrInvalidInput, in.Action)
	}
	switch in.Action {
	case "raise_incident":
		if inc == nil || strings.TrimSpace(inc.Summary) == "" {
			return fmt.Errorf("%w: raise_incident needs incident with summary, impact and urgency (HIGH, MEDIUM or LOW)", ErrInvalidInput)
		}
		for _, lv := range []*string{&inc.Impact, &inc.Urgency} {
			v, ok := incidentLevels[strings.ToUpper(strings.TrimSpace(*lv))]
			if !ok {
				return fmt.Errorf("%w: incident impact and urgency are HIGH, MEDIUM or LOW (ServiceNow's 1, 2 or 3)", ErrInvalidInput)
			}
			*lv = v
		}
	case "link_incident", "unlink_incident", "close_incident":
		if inc == nil || strings.TrimSpace(inc.Number) == "" {
			return fmt.Errorf("%w: %s needs incident.number", ErrInvalidInput, in.Action)
		}
		inc.Number = strings.ToUpper(strings.TrimSpace(inc.Number))
	}
	return nil
}

// incidentAction runs one of the incident writes on a check of snapshot sn. Each is a dry run unless apply; raise and close change the
// customer's ServiceNow and need confirm (raise: the check id; close: the incident number).
func incidentAction(ctx context.Context, s *fwd.Session, in manageChecksInput, sn *forward.Snapshot, cx result.Context) (result.Result, error) {
	sid := string(sn.ID)
	inc := in.Incident
	limits := []string{checkIncidentsLimit}
	if cx.State == "predicted" {
		limits = append(limits, "this is a predicted snapshot: Forward allows check incidents on one only when the org property PREDICT_UNSANDBOXED is on")
	}
	ev := func(mode string, extra map[string]any) []result.Evidence {
		d := map[string]any{"action": in.Action, "mode": mode, "snapshot_id": sid}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvPolicy, "manageCheckIncidents", cx.SnapshotID, d, "")}
	}
	linked := func() ([]map[string]any, error) {
		links, _, err := s.Client.Integrations.ListCheckIncidents(ctx, sid, allCheckTypes)
		if err != nil {
			return nil, err
		}
		rows := []map[string]any{}
		for _, l := range links {
			if in.CheckID == "" || string(l.CheckID) == in.CheckID {
				rows = append(rows, incidentLinkRow(l))
			}
		}
		return rows, nil
	}
	hasNumber := func(rows []map[string]any, n string) bool {
		for _, r := range rows {
			if strings.EqualFold(fmt.Sprint(r["number"]), n) {
				return true
			}
		}
		return false
	}
	before := map[string]any{}
	var check *forward.CheckDetail
	if in.CheckID != "" {
		d, err := s.CheckDetail(ctx, sid, in.CheckID)
		if fwd.NotFound(err) {
			return result.NewUnknown(manageChecksName, fmt.Sprintf("Snapshot %s has no check %s; nothing was changed", sid, in.CheckID), cx,
				[]string{"no such check on this snapshot (ids are matched exactly)"}, result.Options{NextActions: []string{"check-network-compliance"}})
		}
		if err != nil {
			return result.Result{}, err
		}
		check = d
		before["check"] = map[string]any{"id": in.CheckID, "name": d.Name, "status": d.Status}
	}
	rows, lerr := linked()
	if lerr == nil {
		before["incidents"] = rows
	} else {
		before["incidents"] = "could not be read (" + lerr.Error() + ")"
	}
	ch := result.Change{Target: fmt.Sprintf("check %s on snapshot %s", in.CheckID, sid), Before: before}
	var do func() (any, error)
	var verb, confirmWith string
	switch in.Action {
	case "raise_incident":
		if check != nil && check.Enabled != nil && !*check.Enabled {
			return result.Build(manageChecksName, result.Failed, fmt.Sprintf("Refused, nothing was sent: check %q is deactivated, and Forward raises an incident only for an enabled failing check", check.Name),
				result.Deterministic, cx, result.Options{Mode: modeOf(in.Apply), Limits: limits, Evidence: ev(modeOf(in.Apply), map[string]any{"check": before["check"], "refused": "check deactivated"})})
		}
		if check != nil && check.Status != "FAIL" {
			return result.Build(manageChecksName, result.Failed, fmt.Sprintf("Refused, nothing was sent: check %q is %s, and Forward raises an incident only for a check failing in this snapshot and every later one", check.Name, check.Status),
				result.Deterministic, cx, result.Options{Mode: modeOf(in.Apply), Limits: limits, Evidence: ev(modeOf(in.Apply), map[string]any{"check": before["check"], "refused": "check not failing"})})
		}
		if lerr != nil {
			limits = append(limits, "the links of this check could not be read, so whether it already has an open incident (Forward then refuses a second) is not known")
		} else {
			// Seen live (a ServiceNow write test): the dry run said ok for a check that already had an open incident, and apply was then refused with a 400
			// ("already linked to incident"), so a linked open incident is refused here, before anything is sent. A linked incident that is resolved,
			// closed or canceled is not refused: whether Forward then allows a new one has not been seen.
			for _, r := range rows {
				if st := strings.ToLower(fmt.Sprint(r["state"])); st != "closed" && st != "resolved" && st != "canceled" {
					return result.Build(manageChecksName, result.Failed, fmt.Sprintf("Refused, nothing was sent: check %s is already linked to incident %v (%v), and Forward raises one incident per check; unlink it (unlink_incident), or close it in ServiceNow and run refresh_incidents (the state shown is Forward's stored copy)", in.CheckID, r["number"], r["state"]),
						result.Deterministic, cx, result.Options{Mode: modeOf(in.Apply), Limits: limits, Evidence: ev(modeOf(in.Apply), map[string]any{"check": before["check"], "incidents": rows, "refused": "check already linked to an open incident"})})
				}
			}
		}
		input := forward.ServiceNowIncidentInput{Summary: strings.TrimSpace(inc.Summary), Details: strings.TrimSpace(inc.Details), Impact: inc.Impact, Urgency: inc.Urgency}
		ch.Action, verb, confirmWith = "raise_servicenow_incident", "raise a ServiceNow incident for", in.CheckID
		ch.Target += ", ServiceNow"
		ch.After = map[string]any{"summary": input.Summary, "details": input.Details, "impact": input.Impact, "urgency": input.Urgency}
		ch.Undo = "close it (action close_incident with its number) or cancel it in ServiceNow; unlink_incident removes only Forward's link"
		do = func() (any, error) {
			l, _, e := s.Client.Integrations.AddCheckIncident(ctx, sid, in.CheckID, input)
			if e != nil {
				return nil, fmt.Errorf("%w; Forward may have created the incident in ServiceNow before failing: list them with check-network-compliance view incidents before retrying", e)
			}
			if l == nil {
				return map[string]any{}, nil
			}
			return incidentLinkRow(*l), nil
		}
	case "link_incident":
		if lerr == nil && hasNumber(rows, inc.Number) {
			return result.Build(manageChecksName, result.OK, fmt.Sprintf("Incident %s is already linked to check %s; nothing to change", inc.Number, in.CheckID), result.Deterministic, cx,
				result.Options{Mode: modeOf(in.Apply), Limits: limits, Evidence: ev(modeOf(in.Apply), map[string]any{"already_linked": true})})
		}
		ch.Action, verb, ch.Reversible = "link_servicenow_incident", "link incident "+inc.Number+" to", true
		ch.After = map[string]any{"incident": inc.Number}
		ch.Undo = fmt.Sprintf("action unlink_incident with check_id %s and incident.number %s", in.CheckID, inc.Number)
		do = func() (any, error) {
			l, _, e := s.Client.Integrations.LinkCheckIncident(ctx, sid, in.CheckID, inc.Number)
			if e != nil || l == nil {
				return nil, e
			}
			return incidentLinkRow(*l), nil
		}
	case "unlink_incident":
		if lerr == nil && !hasNumber(rows, inc.Number) {
			return result.Build(manageChecksName, result.OK, fmt.Sprintf("Incident %s is not linked to check %s; nothing to change", inc.Number, in.CheckID), result.Deterministic, cx,
				result.Options{Mode: modeOf(in.Apply), Limits: limits, Evidence: ev(modeOf(in.Apply), map[string]any{"not_linked": true})})
		}
		ch.Action, verb, ch.Reversible = "unlink_servicenow_incident", "unlink incident "+inc.Number+" from", true
		ch.After = map[string]any{"incident": inc.Number, "linked": false}
		ch.Undo = fmt.Sprintf("action link_incident with check_id %s and incident.number %s; the incident itself stays in ServiceNow", in.CheckID, inc.Number)
		do = func() (any, error) {
			_, e := s.Client.Integrations.UnlinkCheckIncident(ctx, sid, in.CheckID, inc.Number)
			return nil, e
		}
	case "close_incident":
		if lerr == nil {
			for _, r := range rows {
				if strings.EqualFold(fmt.Sprint(r["number"]), inc.Number) {
					return result.Build(manageChecksName, result.Failed, fmt.Sprintf("Refused, nothing was sent: incident %s is still linked to check %v, and Forward refuses to close a linked incident; unlink it first (action unlink_incident, check_id %v)", inc.Number, r["check_id"], r["check_id"]),
						result.Deterministic, cx, result.Options{Mode: modeOf(in.Apply), Limits: limits, Evidence: ev(modeOf(in.Apply), map[string]any{"incidents": rows, "refused": "still linked"})})
				}
			}
		} else {
			limits = append(limits, "the check links could not be read, so whether the incident is still linked (Forward then refuses to close it) is not known")
		}
		ch.Target = "ServiceNow incident " + inc.Number
		if cur, _, e := s.Client.Integrations.GetServiceNowIncident(ctx, inc.Number); e == nil && cur != nil {
			before["incident"] = map[string]any{"number": cur.Number, "state": cur.State, "summary": cur.Summary, "url": cur.URL}
			if strings.EqualFold(cur.State, "closed") {
				return result.Build(manageChecksName, result.OK, fmt.Sprintf("Incident %s is already closed; nothing to change", inc.Number), result.Deterministic, cx,
					result.Options{Mode: modeOf(in.Apply), Limits: limits, Evidence: ev(modeOf(in.Apply), map[string]any{"incident": before["incident"]})})
			}
		} else if e != nil {
			before["incident"] = "could not be read from ServiceNow (" + e.Error() + ")"
		}
		ch.Action, verb, confirmWith = "close_servicenow_incident", "close ServiceNow incident "+inc.Number+" (a change to the org's ServiceNow record)", inc.Number
		ch.After = map[string]any{"state": "Closed"}
		ch.Undo = "none from Forward or here: reopen it in ServiceNow"
		do = func() (any, error) {
			if _, e := s.Client.Integrations.CloseServiceNowIncident(ctx, inc.Number); e != nil {
				return nil, e
			}
			if cur, _, e := s.Client.Integrations.GetServiceNowIncident(ctx, inc.Number); e == nil && cur != nil {
				return map[string]any{"number": cur.Number, "state": cur.State}, nil
			} else if e != nil {
				return map[string]any{"number": inc.Number, "state": "unknown: could not be read back (" + e.Error() + ")"}, nil
			}
			return map[string]any{"number": inc.Number}, nil
		}
	case "refresh_incidents":
		ch.Target = "the incidents of snapshot " + sid + "'s checks"
		ch.Action, verb, ch.Reversible = "refresh_servicenow_incidents", "re-read from ServiceNow the state of every incident linked to the checks of", true
		ch.Undo = "nothing to undo: Forward re-reads each incident's state; ServiceNow is not changed"
		do = func() (any, error) {
			_, e := s.Client.Integrations.RefreshCheckIncidents(ctx, sid)
			return nil, e
		}
	}
	mode := modeOf(in.Apply)
	if !in.Apply {
		msg := fmt.Sprintf("Dry run: would %s %s. Nothing was changed; run again with apply=true", verb, ch.Target)
		if confirmWith != "" {
			msg += fmt.Sprintf(" and confirm=%q", confirmWith)
		}
		return result.Build(manageChecksName, result.OK, msg, result.Deterministic, cx, result.Options{Mode: mode, Changes: []result.Change{ch}, Limits: limits, Evidence: ev(mode, nil), NextActions: []string{"check-network-compliance"}})
	}
	if confirmWith != "" && strings.TrimSpace(in.Confirm) != confirmWith {
		return result.Build(manageChecksName, result.Failed, fmt.Sprintf("Refused, nothing was sent: %s needs confirm=%q", in.Action, confirmWith), result.Deterministic, cx,
			result.Options{Mode: mode, Limits: limits, Evidence: ev(mode, map[string]any{"refused": "confirm missing"})})
	}
	got, err := do()
	if err != nil {
		return result.Result{}, fmt.Errorf("%s failed: %w", in.Action, err)
	}
	ch.Applied = true
	status, conf := result.OK, result.Deterministic
	finding := fmt.Sprintf("Applied: %s %s", verb, ch.Target)
	after := map[string]any{"result": got}
	switch in.Action {
	case "raise_incident", "link_incident", "unlink_incident":
		now, e := linked()
		want := in.Action != "unlink_incident"
		n := ""
		if inc != nil {
			n = inc.Number
		}
		if m, ok := got.(map[string]any); ok && in.Action == "raise_incident" {
			n = fmt.Sprint(m["number"])
			finding = fmt.Sprintf("Raised ServiceNow incident %s for check %s on snapshot %s", n, in.CheckID, sid)
		}
		switch {
		case e != nil:
			status, conf = result.Unknown, result.NoBasis
			finding += "; the links could not be read back (" + e.Error() + ")"
		case hasNumber(now, n) != want:
			status, conf = result.Unknown, result.NoBasis
			finding += fmt.Sprintf("; but the links read back do not show incident %s %s", n, map[bool]string{true: "linked", false: "unlinked"}[want])
		}
		after["incidents"] = now
	case "close_incident":
		if m, ok := got.(map[string]any); ok && !strings.EqualFold(fmt.Sprint(m["state"]), "closed") {
			status, conf = result.Unknown, result.NoBasis
			finding += fmt.Sprintf("; ServiceNow reads it back as %v, not Closed", m["state"])
		}
	}
	return result.Build(manageChecksName, status, finding, conf, cx, result.Options{Mode: mode, Changes: []result.Change{ch}, Limits: limits, Evidence: ev(mode, after), NextActions: []string{"check-network-compliance"}})
}
