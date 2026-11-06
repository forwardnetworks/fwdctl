package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// ServiceNow change requests of a Predict change set: edit-change-set lifecycle raise_change_request, attach_to_change_request and
// download_change_request_attachment, and the linked requests in verify-change view history. Forward has four routes for them (create,
// get, attach, download) and no route to unlink, delete or list one; the only list is the change set summary's refs.

const serviceNowChangeRequestLimit = "ServiceNow change requests: Forward has no route to delete or unlink one, so a raised request or an uploaded attachment is undone in ServiceNow, not here. " +
	"Raising and attaching need the org properties PREDICT_MODELING and SERVICENOW_CHANGE_REQUESTS (off by default, not settable by an org administrator), the org's ServiceNow integration " +
	"(inspect-platform area integrations), the deployment's outbound connections and the change-set permission; reading and downloading need only the permission and the outbound connections. " +
	"Preview routes, read from Forward's source by the SDK session, not yet run against a live ServiceNow"

// serviceNowAttachmentTypes are the file types Forward accepts on a change request (it answers 400 for others).
var serviceNowAttachmentTypes = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".pdf": true, ".xlsx": true, ".docx": true, ".csv": true}

func checkAttachmentFile(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if !serviceNowAttachmentTypes[ext] {
		return fmt.Errorf("lifecycle.path %s: Forward accepts only png, jpg, jpeg, pdf, xlsx, docx and csv attachments", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("lifecycle.path %s cannot be read: %v", path, err)
	}
	if fi.IsDir() || fi.Size() == 0 || fi.Size() > forward.ServiceNowMaxAttachmentBytes {
		return fmt.Errorf("lifecycle.path %s: an attachment is a file of 1 byte to 25 MB", path)
	}
	return nil
}

type changeRequestPlan struct {
	verb string
	do   func() (any, error)
}

// linkedRequests reads the change requests the change set summary lists (number and sys id).
func linkedRequests(ctx context.Context, s *fwd.Session, networkID, changeSetID string) ([]forward.ServiceNowChangeRequestRef, error) {
	sum, _, err := s.Client.Predict.GetChangeSetSummary(ctx, networkID, changeSetID)
	if err != nil {
		return nil, err
	}
	if sum == nil {
		return nil, nil
	}
	return sum.ServiceNowChangeRequests, nil
}

func refRows(refs []forward.ServiceNowChangeRequestRef) []map[string]any {
	rows := make([]map[string]any, 0, len(refs))
	for _, r := range refs {
		rows = append(rows, map[string]any{"number": r.Number, "sys_id": r.SysID})
	}
	return rows
}

func hasRef(refs []forward.ServiceNowChangeRequestRef, sysID string) bool {
	for _, r := range refs {
		if r.SysID == sysID {
			return true
		}
	}
	return false
}

func changeRequestRow(cr *forward.ServiceNowChangeRequest) map[string]any {
	atts := make([]map[string]any, 0, len(cr.Attachments))
	for _, a := range cr.Attachments {
		atts = append(atts, map[string]any{"file_name": a.FileName, "file_sys_id": a.FileSysID})
	}
	return map[string]any{"number": cr.Number, "sys_id": cr.SysID, "summary": cr.Summary, "impact": cr.Impact, "priority": cr.Priority,
		"state": firstNonEmpty(cr.State, "(not reported)"), "url": cr.URL, "attachments": atts}
}

// changeRequestOp plans one of the three change-request ops. It reads the change set's linked requests (and, to raise, whether the org has
// ServiceNow set up) in the dry run too. A non-nil result is a refusal: nothing was or will be sent.
func changeRequestOp(ctx context.Context, s *fwd.Session, in draftChangeSetInput, op string, ch *result.Change, before map[string]any, cx result.Context) (changeRequestPlan, *result.Result, error) {
	l, id := in.Lifecycle, in.ChangeSetID
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	refuse := func(status result.Status, finding string, limits ...string) (changeRequestPlan, *result.Result, error) {
		limits = append(limits, serviceNowChangeRequestLimit)
		conf := result.Deterministic
		if status == result.Unknown {
			conf = result.NoBasis
		}
		r, err := result.Build(draftChangeSetName, status, finding, conf, cx, result.Options{Mode: mode, Limits: limits, NextActions: []string{"verify-change", "inspect-platform"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetLifecycle", nil, map[string]any{"mode": mode, "op": op, "change_set": before, "refused": finding}, finding)}})
		if err != nil {
			return changeRequestPlan{}, nil, err
		}
		return changeRequestPlan{}, &r, nil
	}
	refs, rerr := linkedRequests(ctx, s, in.NetworkID, id)
	if rerr == nil {
		before["servicenow_change_requests"] = refRows(refs)
	} else {
		before["servicenow_change_requests"] = "could not be read (" + rerr.Error() + ")"
	}
	ch.Reversible = false
	switch op {
	case "raise_change_request":
		cr := l.ChangeRequest
		sn, configured, _, err := s.Client.Integrations.GetServiceNow(ctx)
		switch {
		case err == nil && !configured:
			return refuse(result.Failed, fmt.Sprintf("Refused, nothing was sent: this organization has no ServiceNow integration, so Forward cannot raise a change request for change set %s", id),
				"set one up with edit-platform area integrations name servicenow (an org administrator), then raise the request")
		case err == nil && sn != nil:
			before["servicenow"] = map[string]any{"configured": true, "instance_url": sn.InstanceURL, "enabled": sn.Enabled}
			if !sn.Enabled {
				before["servicenow_note"] = "the org's ServiceNow integration reads as not enabled; Forward may refuse the request (whether enabled gates change requests is not known from here)"
			}
		case err != nil:
			before["servicenow"] = "whether the org has ServiceNow set up could not be read (" + err.Error() + "); Forward refuses the request if it has none"
		}
		input := forward.ServiceNowChangeRequestInput{Summary: strings.TrimSpace(cr.Summary), Details: strings.TrimSpace(cr.Details), Impact: cr.Impact, Priority: cr.Priority, WorkNotes: strings.TrimSpace(cr.WorkNotes)}
		ch.Action, ch.Target = "raise_servicenow_change_request", ch.Target+", ServiceNow"
		ch.After = map[string]any{"summary": input.Summary, "details": input.Details, "impact": input.Impact, "priority": firstNonEmpty(input.Priority, "(ServiceNow's default)"), "work_notes": input.WorkNotes}
		ch.Undo = "none from Forward or here: the request is a ticket in the org's ServiceNow, so cancel it there (its number and URL are in the result); Forward has no route to unlink it from the change set"
		return changeRequestPlan{verb: "raise a ServiceNow change request for", do: func() (any, error) {
			got, _, e := s.Client.Integrations.CreateChangeSetChangeRequest(ctx, in.NetworkID, id, input)
			if e != nil {
				// Forward creates the ticket before it links it, so a failure may still have left one: report any request linked since.
				if rerr == nil {
					if now, le := linkedRequests(ctx, s, in.NetworkID, id); le == nil {
						for _, r := range now {
							if !hasRef(refs, r.SysID) {
								return nil, fmt.Errorf("%w; but change request %s (sys_id %s) is now linked to the change set, so a ticket WAS raised", e, r.Number, r.SysID)
							}
						}
					}
				}
				return nil, fmt.Errorf("%w; Forward creates the ticket in ServiceNow before linking it, so check ServiceNow before retrying", e)
			}
			out := changeRequestRow(got)
			if now, le := linkedRequests(ctx, s, in.NetworkID, id); le == nil {
				out["linked_to_change_set"] = hasRef(now, got.SysID)
			} else {
				out["linked_to_change_set"] = "unknown: the summary could not be read back (" + le.Error() + ")"
			}
			return out, nil
		}}, nil, nil
	case "attach_to_change_request":
		if rerr != nil {
			return refuse(result.Unknown, fmt.Sprintf("Nothing was sent: the change set %s summary could not be read (%v), so whether request %s is linked to it is unknown", id, rerr, l.SysID))
		}
		if !hasRef(refs, l.SysID) {
			return refuse(result.Failed, fmt.Sprintf("Refused, nothing was sent: change set %s has no linked change request with sys_id %s", id, l.SysID),
				"verify-change view history lists the change set's requests and their sys ids")
		}
		name := filepath.Base(l.Path)
		fi, ferr := os.Stat(l.Path)
		if ferr != nil {
			return refuse(result.Failed, fmt.Sprintf("Refused, nothing was sent: %s cannot be read (%v)", l.Path, ferr))
		}
		priorIDs := map[string]bool{}
		priorKnown := false
		if cur, _, e := s.Client.Integrations.GetChangeSetChangeRequest(ctx, in.NetworkID, id, l.SysID); e == nil && cur != nil {
			before["change_request"] = changeRequestRow(cur)
			priorKnown = true
			for _, a := range cur.Attachments {
				priorIDs[a.FileSysID] = true
			}
		} else if e != nil {
			before["change_request"] = "could not be read (" + e.Error() + ")"
		}
		ch.Action, ch.Target = "attach_to_servicenow_change_request", ch.Target+", ServiceNow change request "+l.SysID
		ch.After = map[string]any{"file_name": name, "bytes": fi.Size()}
		if len(name) > forward.ServiceNowMaxAttachmentFileName {
			ch.After.(map[string]any)["note"] = "Forward shortens a file name past 100 characters"
		}
		ch.Undo = "none from Forward or here: remove the attachment in ServiceNow"
		return changeRequestPlan{verb: "attach " + name + " to ServiceNow change request " + l.SysID + " of", do: func() (any, error) {
			content, e := os.ReadFile(l.Path)
			if e != nil {
				return nil, e
			}
			if _, e = s.Client.Integrations.AttachToChangeSetChangeRequest(ctx, in.NetworkID, id, l.SysID, name, content); e != nil {
				return nil, e
			}
			out := map[string]any{"file_name": name, "bytes": len(content)}
			if cur, _, ge := s.Client.Integrations.GetChangeSetChangeRequest(ctx, in.NetworkID, id, l.SysID); ge == nil && cur != nil {
				out["change_request"] = changeRequestRow(cur)
				// A new attachment is one whose file_sys_id was not there before; without the earlier list, fall back to the name.
				seen := false
				for _, a := range cur.Attachments {
					switch {
					case priorKnown && !priorIDs[a.FileSysID]:
						seen = true
						out["file_sys_id"] = a.FileSysID
					case !priorKnown && (a.FileName == name || len(name) > forward.ServiceNowMaxAttachmentFileName && strings.HasPrefix(name, strings.TrimSuffix(a.FileName, filepath.Ext(a.FileName)))):
						seen = true
					}
				}
				out["attachment_listed"] = seen
			} else if ge != nil {
				out["attachment_listed"] = "unknown: the request could not be read back (" + ge.Error() + ")"
			}
			return out, nil
		}}, nil, nil
	default: // download_change_request_attachment
		if rerr == nil && !hasRef(refs, l.SysID) {
			return refuse(result.Unknown, fmt.Sprintf("Nothing was downloaded: change set %s has no linked change request with sys_id %s", id, l.SysID),
				"verify-change view history lists the change set's requests and their attachments")
		}
		ch.Action, ch.Target = "download_change_request_attachment", "local file "+l.Path
		ch.Before, ch.After = map[string]any{"path": l.Path, "exists": false}, map[string]any{"path": l.Path, "change_request_sys_id": l.SysID, "file_sys_id": l.FileSysID}
		ch.Reversible, ch.Undo = true, "delete the file; nothing in Forward or ServiceNow changes (Forward records the download in its audit log)"
		return changeRequestPlan{verb: "download attachment " + l.FileSysID + " of ServiceNow change request " + l.SysID + " of", do: func() (any, error) {
			f, e := os.OpenFile(l.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if e != nil {
				return nil, e
			}
			cw := &countWriter{w: f}
			_, e = s.Client.Integrations.DownloadChangeSetChangeRequestAttachment(ctx, in.NetworkID, id, l.SysID, l.FileSysID, cw)
			if ce := f.Close(); e == nil {
				e = ce
			}
			if e != nil {
				os.Remove(l.Path)
				return nil, e
			}
			return map[string]any{"path": l.Path, "bytes": cw.n}, nil
		}}, nil, nil
	}
}

type countWriter struct {
	w interface{ Write([]byte) (int, error) }
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// changeRequestDetails is the linked-requests part of verify-change view history: each request the summary lists, read from ServiceNow
// through Forward (state, URL, attachments). At most maxChangeRequestReads are read; the rest are listed by number only.
const maxChangeRequestReads = 10

func changeRequestDetails(ctx context.Context, s *fwd.Session, networkID, changeSetID string, refs []forward.ServiceNowChangeRequestRef) ([]map[string]any, []string) {
	rows := make([]map[string]any, 0, len(refs))
	var limits []string
	failed := 0
	for i, r := range refs {
		row := map[string]any{"number": r.Number, "sys_id": r.SysID}
		if i >= maxChangeRequestReads {
			row["read"] = false
			rows = append(rows, row)
			continue
		}
		cr, _, err := s.Client.Integrations.GetChangeSetChangeRequest(ctx, networkID, changeSetID, r.SysID)
		if err != nil || cr == nil {
			failed++
			if err != nil {
				row["error"] = err.Error()
			}
			rows = append(rows, row)
			continue
		}
		rows = append(rows, changeRequestRow(cr))
	}
	if len(refs) > maxChangeRequestReads {
		limits = append(limits, fmt.Sprintf("only the first %d linked change requests were read from ServiceNow; the rest are listed by number and sys_id", maxChangeRequestReads))
	}
	if failed > 0 {
		limits = append(limits, fmt.Sprintf("%d linked change request(s) could not be read from ServiceNow (each row says why): reading one goes through the deployment's outbound connections to the org's ServiceNow, so an answer here is not proof the request is gone", failed))
	}
	if len(refs) > 0 {
		limits = append(limits, "linked change requests are read live from ServiceNow through Forward (Preview route, source-read, not yet run against a live ServiceNow); download an attachment with edit-change-set lifecycle download_change_request_attachment")
	}
	return rows, limits
}
