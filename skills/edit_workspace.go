package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editWorkspaceName = "edit-workspace"

func init() { Register(editWorkspaceName, editWorkspace) }

type wsCreate struct {
	Name          string   `json:"name"`
	Note          string   `json:"note"`
	Devices       []string `json:"devices"`
	RetentionDays *int     `json:"retention_days"`
	Omissions     []string `json:"omissions"`
}

type wsEndpoint struct {
	Name         string `json:"name"`
	ProfileID    string `json:"profile_id"`
	CredentialID string `json:"credential_id"`
}

type wsAddEndpoints struct {
	// FromNetwork is where host, port and protocol are read (default: the workspace's parent).
	FromNetwork string       `json:"from_network"`
	Endpoints   []wsEndpoint `json:"endpoints"`
}

type editWorkspaceInput struct {
	NetworkID       string          `json:"network_id"`
	CreateWorkspace *wsCreate       `json:"create_workspace"`
	AddEndpoints    *wsAddEndpoints `json:"add_endpoints"`
	DeleteWorkspace bool            `json:"delete_workspace"`
	ConfirmName     string          `json:"confirm_name"`
	Apply           bool            `json:"apply"`
}

var (
	wsOmissions = map[string]bool{"CUSTOM_COMMANDS": true, "NQE_CHECKS": true, "PREDEFINED_CHECKS": true, "INTENT_CHECKS": true}
	wsNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,79}$`)
)

// credentialCopyNote says what the organization property copy_credentials does to a workspace created now (it is read, never changed here).
func credentialCopyNote(ctx context.Context, s *fwd.Session) string {
	props, err := s.EffectiveOrgConfig(ctx)
	if err != nil {
		return "whether credentials are copied into the workspace could not be read (organization property copy_credentials: " + err.Error() + "); if it is DISABLED a person must create credentials in the workspace in the Forward UI"
	}
	switch v := strings.Trim(strings.ToUpper(props["copy_credentials"]), `"`); v {
	case "ENABLED":
		return "organization property copy_credentials is ENABLED: Forward copies ALL of the parent's stored secrets (every CLI, SNMP, HTTP and cloud credential, jump servers) into the workspace for its lifetime, with the same ids; cloud accounts and vCenters named are copied too"
	case "ENABLED_FOR_ADMINS":
		return "organization property copy_credentials is ENABLED_FOR_ADMINS: ALL of the parent's secrets are copied only if the creator is an organization admin or an admin of the parent; otherwise none"
	case "DISABLED", "":
		return "organization property copy_credentials is DISABLED (the default): no credential, cloud account or vCenter comes across, so a person must create the credential inside the workspace in the Forward UI (never paste a secret into an agent)"
	default:
		return "organization property copy_credentials is " + v + "; read it with inspect-environment"
	}
}

// editWorkspace creates a temporary workspace network under a production network, adds endpoints to a workspace, or deletes a workspace. A workspace is a
// separate network with its own sources, collections and snapshots; it changes nothing in its parent. It never touches a network that is not a workspace.
func editWorkspace(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editWorkspaceInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	n := btoi(in.CreateWorkspace != nil) + btoi(in.AddEndpoints != nil) + btoi(in.DeleteWorkspace)
	if in.NetworkID == "" || n != 1 {
		return result.Result{}, fmt.Errorf("%w: network_id and exactly one of create_workspace (network_id is the parent), add_endpoints (network_id is the workspace) or delete_workspace (network_id is the workspace, with confirm_name)", ErrInvalidInput)
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	nets, err := s.Networks(ctx)
	if err != nil {
		return result.Result{}, err
	}
	byID := map[string]forward.Network{}
	names := map[string]bool{}
	for _, nw := range nets {
		byID[string(nw.ID)] = nw
		names[strings.ToLower(nw.Name)] = true
	}
	target, ok := byID[in.NetworkID]
	evd := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"networks_read": len(nets), "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvCollection, "networks", nil, d, "")}
	}
	refuse := func(msg string) (result.Result, error) {
		return result.Build(editWorkspaceName, result.Failed, "Refused, nothing was changed: "+msg, result.Deterministic, cx,
			result.Options{Mode: mode, Limits: []string{"nothing was sent to Forward"}, Evidence: evd(map[string]any{"refused": msg})})
	}
	if !ok {
		return refuse(fmt.Sprintf("this login sees no network %s", in.NetworkID))
	}
	isWorkspace := target.ParentID != ""

	switch {
	case in.CreateWorkspace != nil:
		c := in.CreateWorkspace
		if isWorkspace {
			return refuse(fmt.Sprintf("%s is itself a workspace; Forward cannot create a workspace under a workspace", in.NetworkID))
		}
		if !wsNameRe.MatchString(c.Name) || names[strings.ToLower(c.Name)] {
			return refuse("name must be 1 to 80 letters, digits, space, _ . - and not already used by any network (names are unique in the organization, ignoring case)")
		}
		if len(c.Devices) == 0 {
			return refuse("a workspace needs at least one classic device of the parent (Forward has no endpoint-only workspace); name the smallest, most harmless one")
		}
		have := map[string]bool{}
		devs, derr := s.ClassicDevices(ctx, in.NetworkID)
		if derr != nil {
			return result.Result{}, derr
		}
		for _, d := range devs {
			have[d.Name] = true
		}
		for _, d := range c.Devices {
			if !have[d] {
				return refuse(fmt.Sprintf("the parent has no classic device %s (names are matched exactly)", d))
			}
		}
		for _, o := range c.Omissions {
			if !wsOmissions[o] {
				return refuse(fmt.Sprintf("omission %s is not one of CUSTOM_COMMANDS, NQE_CHECKS, PREDEFINED_CHECKS, INTENT_CHECKS", o))
			}
		}
		ret := 7
		if c.RetentionDays != nil {
			ret = *c.RetentionDays
		}
		if ret < 1 || ret > 365 {
			return refuse("retention_days is 1 to 365 (omit for the default of 7)")
		}
		r32 := int32(ret)
		req := forward.WorkspaceNetworkRequest{Name: c.Name, Note: c.Note, Devices: c.Devices, Omissions: c.Omissions, RetentionDays: &r32}
		credNote := credentialCopyNote(ctx, s)
		limits := []string{
			"a workspace is a separate network with its own sources, collections and snapshots; nothing in the parent changes",
			"it holds the named classic device(s) only: endpoints are NOT copied (add them with add_endpoints)",
			credNote,
			fmt.Sprintf("it expires after %d day(s); deleting it earlier is delete_workspace", ret),
			"Forward also makes an \"initial subset\" snapshot of the parent's latest snapshot for the copied devices when it can",
		}
		ch := result.Change{Action: "create_workspace", Target: "networks under " + in.NetworkID, After: map[string]any{"name": c.Name, "devices": c.Devices, "retention_days": ret, "omissions": c.Omissions, "note": c.Note},
			Reversible: true, Undo: "delete_workspace the new network id with confirm_name " + c.Name}
		if !in.Apply {
			return result.Build(editWorkspaceName, result.OK, fmt.Sprintf("Dry run: would create workspace %q under network %s holding %d device(s), expiring in %d day(s). Nothing was changed; run again with apply=true", c.Name, in.NetworkID, len(c.Devices), ret),
				result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(map[string]any{"payload": req}), NextActions: []string{"inspect-collection", "edit-collection"}})
		}
		nw, cerr := s.CreateWorkspace(ctx, in.NetworkID, req)
		if cerr != nil {
			return result.Result{}, fmt.Errorf("the workspace was not created, nothing is known to have changed: %w", cerr)
		}
		ch.Applied = true
		ch.After = map[string]any{"id": string(nw.ID), "name": nw.Name, "parent_id": string(nw.ParentID), "retention_days": nw.RetentionDays}
		ch.Undo = fmt.Sprintf("delete_workspace network %s with confirm_name %s", nw.ID, nw.Name)
		return result.Build(editWorkspaceName, result.OK, fmt.Sprintf("Created workspace %q as network %s under %s. Undo: delete_workspace %s with confirm_name %q", nw.Name, nw.ID, in.NetworkID, nw.ID, nw.Name),
			result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(map[string]any{"workspace_id": string(nw.ID), "name": nw.Name, "parent_id": string(nw.ParentID), "held": string(nw.ParentID) == in.NetworkID}),
				NextActions: []string{"inspect-collection", "edit-collection"}})

	case in.AddEndpoints != nil:
		a := in.AddEndpoints
		if !isWorkspace {
			return refuse(fmt.Sprintf("%s is not a workspace; this skill adds endpoints only to a workspace, never to a production network", in.NetworkID))
		}
		if len(a.Endpoints) == 0 || len(a.Endpoints) > maxEndpointsPerRun {
			return refuse(fmt.Sprintf("give 1 to %d endpoints", maxEndpointsPerRun))
		}
		from := firstNonEmpty(a.FromNetwork, string(target.ParentID))
		src, serr := s.Endpoints(ctx, from)
		if serr != nil {
			return result.Result{}, serr
		}
		srcBy := map[string]forward.Endpoint{}
		for _, e := range src {
			srcBy[e.Name] = e
		}
		cur, cerr := s.Endpoints(ctx, in.NetworkID)
		if cerr != nil {
			return result.Result{}, cerr
		}
		exists := map[string]bool{}
		for _, e := range cur {
			exists[e.Name] = true
		}
		byType := map[string][]forward.Endpoint{}
		var changes []result.Change
		var limits []string
		for _, w := range a.Endpoints {
			e, ok := srcBy[w.Name]
			if !ok {
				return refuse(fmt.Sprintf("network %s has no endpoint %s to copy host and protocol from (names are matched exactly)", from, w.Name))
			}
			if exists[w.Name] {
				return refuse(fmt.Sprintf("the workspace already has an endpoint %s", w.Name))
			}
			cred := strings.TrimSpace(w.CredentialID)
			if cred == "" {
				cred = e.CredentialID
				if cred == "" {
					limits = append(limits, fmt.Sprintf("endpoint %s has no credential id in the parent either, so it is added the same way (Forward collects it as it does there); give credential_id to set one", w.Name))
				} else {
					limits = append(limits, fmt.Sprintf("endpoint %s uses the parent endpoint's own credential id: it exists in the workspace only if credentials were copied when the workspace was created (organization property copy_credentials); a missing one shows as a connectivity error on the first collection, not here", w.Name))
				}
			}
			if e.JumpServerID != "" {
				limits = append(limits, fmt.Sprintf("endpoint %s is reached through a jump server in the parent; it is not set here, so it will not collect if it needs one", w.Name))
			}
			ne := forward.Endpoint{Type: e.Type, Name: e.Name, Host: e.Host, Port: e.Port, CredentialID: cred, ProfileID: firstNonEmpty(w.ProfileID, e.ProfileID)}
			if strings.EqualFold(e.Type, "CLI") { // protocol and the large-RTT flag exist on CLI endpoints only; the SDK refuses them elsewhere
				ne.Protocol, ne.LargeRTT = e.Protocol, e.LargeRTT
			}
			byType[e.Type] = append(byType[e.Type], ne)
			changes = append(changes, result.Change{Action: "add_endpoint", Target: "workspace " + in.NetworkID + " endpoint " + e.Name, After: map[string]any{"type": ne.Type, "host": ne.Host, "port": ne.Port, "protocol": ne.Protocol, "profile_id": ne.ProfileID, "credential_set": cred != ""},
				Reversible: true, Undo: "delete the endpoint (it goes with the workspace)"})
		}
		limits = append(limits, "host, port and protocol are read from "+from+"; the credential id is yours and is never read back or shown", "nothing is collected until a collection runs in the workspace (edit-collection with the workspace as network_id)")
		if !in.Apply {
			return result.Build(editWorkspaceName, result.OK, fmt.Sprintf("Dry run: would add %d endpoint(s) to workspace %s. Nothing was changed; run again with apply=true", len(changes), in.NetworkID),
				result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"workspace": in.NetworkID, "from_network": from}), NextActions: []string{"edit-collection"}})
		}
		types := make([]string, 0, len(byType))
		for t := range byType {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			if aerr := s.AddEndpoints(ctx, in.NetworkID, t, byType[t]); aerr != nil {
				return result.Result{}, fmt.Errorf("adding the %s endpoints failed; some may have been added: %w", t, aerr)
			}
		}
		for i := range changes {
			changes[i].Applied = true
		}
		now, rerr := s.Endpoints(ctx, in.NetworkID)
		if rerr != nil {
			return result.Result{}, fmt.Errorf("the endpoints were sent but reading them back failed, so it is not proven: %w", rerr)
		}
		got := map[string]string{}
		for _, e := range now {
			got[e.Name] = e.ProfileID
		}
		for _, w := range a.Endpoints {
			if _, ok := got[w.Name]; !ok {
				return result.Build(editWorkspaceName, result.Failed, fmt.Sprintf("Forward accepted the call but the workspace does not list endpoint %s", w.Name), result.Deterministic, cx,
					result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"held": false})})
			}
		}
		return result.Build(editWorkspaceName, result.OK, fmt.Sprintf("Added %d endpoint(s) to workspace %s", len(changes), in.NetworkID), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"held": true}), NextActions: []string{"edit-collection", "inspect-collection"}})

	default: // delete
		if !isWorkspace {
			return refuse(fmt.Sprintf("%s is not a workspace; this skill never deletes a production network", in.NetworkID))
		}
		if in.ConfirmName != target.Name {
			return refuse(fmt.Sprintf("confirm_name must be the workspace's exact name; network %s is named %q", in.NetworkID, target.Name))
		}
		// only the login's own workspaces: the creator Forward records must be this login. A workspace made by someone else is left alone (an organization administrator does that in Forward).
		me, merr := session(ctx, s)
		if merr != nil {
			return result.Result{}, fmt.Errorf("reading this login to check it created the workspace: %w", merr)
		}
		if !strings.EqualFold(target.Creator, me.User.Username) && !strings.EqualFold(target.Creator, me.User.Email) {
			return refuse(fmt.Sprintf("workspace %s was created by %q, not by this login; this skill deletes only workspaces its own login created", in.NetworkID, target.Creator))
		}
		ch := result.Change{Action: "delete_workspace", Target: "workspace " + in.NetworkID, Before: map[string]any{"name": target.Name, "parent_id": string(target.ParentID), "creator": target.Creator, "created_at": target.CreatedAt},
			Reversible: false, Undo: "none: its snapshots, sources and endpoints are deleted with it; create a new workspace to start again"}
		limits := []string{"deleting a workspace removes its own snapshots, sources and endpoints; the parent network is not touched",
			"checked before deleting: the network is a workspace (it has a parent), its recorded creator is this login, and confirm_name is its exact name; a production network is never deleted"}
		if !in.Apply {
			return result.Build(editWorkspaceName, result.OK, fmt.Sprintf("Dry run: would delete workspace %q (network %s, created by %s). Nothing was changed; run again with apply=true", target.Name, in.NetworkID, target.Creator),
				result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(map[string]any{"workspace": in.NetworkID, "name": target.Name})})
		}
		if derr := s.DeleteNetwork(ctx, in.NetworkID); derr != nil {
			if errors.Is(derr, fwd.ErrDeletionRefused) {
				return refuse("the host running this skill does not allow deleting networks (" + derr.Error() + ")")
			}
			return result.Result{}, fmt.Errorf("the delete failed, nothing is known to have changed: %w", derr)
		}
		ch.Applied = true
		after, rerr := s.Networks(ctx)
		if rerr != nil {
			return result.Result{}, fmt.Errorf("the delete was sent but reading the networks back failed, so it is not proven: %w", rerr)
		}
		for _, nw := range after {
			if string(nw.ID) == in.NetworkID {
				return result.Build(editWorkspaceName, result.Failed, "Forward accepted the delete but the workspace is still listed", result.Deterministic, cx,
					result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(map[string]any{"held": false})})
			}
		}
		return result.Build(editWorkspaceName, result.OK, fmt.Sprintf("Deleted workspace %q (network %s)", target.Name, in.NetworkID), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(map[string]any{"held": true})})
	}
}
