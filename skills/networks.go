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
	detail := map[string]any{"total": total, "offset": in.Offset, "returned": end - in.Offset, "networks": rows[in.Offset:end]}
	return result.Build(networksName, result.OK, fmt.Sprintf("%d network(s) returned (%d match)", end-in.Offset, total), result.Deterministic, cx,
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
