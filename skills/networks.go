package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const networksName = "inspect-networks"

func init() { Register(networksName, listNetworks) }

type networksInput struct {
	View   string `json:"view"`
	Name   string `json:"name"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

const (
	defaultNetworksLimit = 25
	maxNetworksLimit     = 200
)

// listNetworks is the one account-level skill: it is about the login, not about a network, so its result has scope "account"
// and no network_id. Every other skill needs the id this one finds.
func listNetworks(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in networksInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Limit <= 0 {
		in.Limit = defaultNetworksLimit
	}
	in.Limit = min(in.Limit, maxNetworksLimit)
	if in.View != "" && in.View != "networks" && in.View != "workspaces" && in.View != "primary" {
		return result.Result{}, fmt.Errorf("%w: view is networks (the default), primary (networks without the workspaces) or workspaces", ErrInvalidInput)
	}
	onlyWorkspaces := in.View == "workspaces"
	onlyPrimary := in.View == "primary"
	cx := result.Context{Scope: "account", State: "current"}
	nets, err := s.Networks(ctx)
	if err != nil {
		return result.Result{}, err
	}
	want := strings.ToLower(strings.TrimSpace(in.Name))
	var rows []map[string]any
	workspaces := 0
	for _, n := range nets {
		if want != "" && !strings.Contains(strings.ToLower(n.Name), want) && string(n.ID) != want {
			continue
		}
		ws := string(n.ParentID) != ""
		if onlyWorkspaces && !ws {
			continue
		}
		if onlyPrimary && ws {
			continue
		}
		if ws {
			workspaces++
		}
		row := map[string]any{"id": string(n.ID), "name": n.Name, "workspace": ws}
		if ws {
			row["parent_id"] = string(n.ParentID)
		}
		if n.Creator != "" {
			row["creator"] = n.Creator
		}
		if n.CreatedAt != "" {
			row["created_at"] = n.CreatedAt
		}
		if n.Note != "" {
			row["note"] = oneLineNote(n.Note)
		}
		if ws && n.RetentionDays != nil {
			row["retention_days"] = *n.RetentionDays
		}
		if ws && n.SecondsToExpiry != nil {
			row["seconds_to_expiry"] = *n.SecondsToExpiry
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["name"].(string) < rows[j]["name"].(string) })
	total := len(rows)
	if total == 0 {
		why := "this login can see no networks"
		if want != "" {
			why = fmt.Sprintf("none of the %d visible network(s) matches %q", len(nets), in.Name)
		}
		return result.NewUnknown(networksName, "No networks were returned", cx,
			[]string{why + "; that is a permissions gap, a wrong name or an empty account, and this skill cannot tell which"}, result.Options{})
	}
	var limits []string
	if in.Offset >= total {
		return result.NewUnknown(networksName, fmt.Sprintf("Offset %d is beyond the %d networks", in.Offset, total), cx,
			[]string{fmt.Sprintf("offset %d is past the end of the %d networks", in.Offset, total)}, result.Options{})
	}
	end := min(in.Offset+in.Limit, total)
	var omitted []result.Omission
	if in.Offset > 0 || end < total {
		omitted = append(omitted, result.Omission{What: "networks", Total: total, Shown: end - in.Offset, From: in.Offset, Paged: true, Next: pageNext(end, total)})
	}
	if workspaces > 0 {
		limits = append(limits, fmt.Sprintf("%d of them are workspaces (copies of another network made for a change); use the parent network's id for questions about the real network", workspaces))
	}
	page := rows[in.Offset:end]
	visibleWS := 0
	for _, n := range nets {
		if string(n.ParentID) != "" {
			visibleWS++
		}
	}
	finding := fmt.Sprintf("%d network(s) returned (%d match; the login sees %d: %d networks and %d workspaces)", end-in.Offset, total, len(nets), len(nets)-visibleWS, visibleWS)
	if end < total || in.Offset > 0 {
		finding += fmt.Sprintf(". This is a page, not the whole list: %d of %d shown, next offset=%d", end-in.Offset, total, end)
	}
	if onlyWorkspaces {
		// one snapshot list per workspace on the page, so the reads are bounded by limit
		for _, row := range page {
			snaps, serr := s.Snapshots(ctx, row["id"].(string))
			if serr != nil {
				row["snapshots_error"] = serr.Error()
				continue
			}
			byTrigger := map[string]int{}
			latestDevices := 0
			latest := ""
			for _, sn := range snaps {
				trig := sn.ProcessingTrigger
				if trig == "" {
					trig = "(none)"
				}
				byTrigger[trig]++
				if at := snapCreated(sn); at >= latest {
					latest, latestDevices = at, sn.TotalDevices
				}
			}
			row["snapshots"], row["snapshots_by_trigger"], row["devices_in_latest_snapshot"] = len(snaps), byTrigger, latestDevices
		}
		limits = append(limits, "snapshot counts by trigger and the device count of the newest snapshot are read per workspace for this page only (limit and offset page them); retention and expiry are Forward's")
		finding = fmt.Sprintf("%d workspace(s) returned (%d match)", end-in.Offset, total)
	}
	detail := map[string]any{"total": total, "offset": in.Offset, "returned": end - in.Offset, "networks": page}
	return result.Build(networksName, result.OK, finding, result.Deterministic, cx,
		result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-inventory", "investigate-collection-failure", "inspect-vulnerabilities"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "listNetworks", nil, detail, fmt.Sprintf("%d networks", total))}})
}

func oneLineNote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
