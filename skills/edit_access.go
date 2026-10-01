package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
)

const editAccessName = "edit-access"

func init() { Register(editAccessName, editAccess) }

type editAccessInput struct {
	// Action: create_user, set_user, delete_user, reset_2fa, set_org_admin, set_network_role, create_group, update_group, delete_group.
	Action   string `json:"action"`
	User     string `json:"user"`  // id, username or email (create_user: the new user's email, also the username)
	Group    string `json:"group"` // id or name (create_group: the new name)
	Password string `json:"password"`
	Enabled  *bool  `json:"enabled"`
	Admin    *bool  `json:"admin"` // set_org_admin: true grants, false revokes
	// NetworkID and Role set a role on one network for a user or group; Role "none" removes it.
	NetworkID string `json:"network_id"`
	Role      string `json:"role"`
	// IdentityProviderGroups and NetworkRoles define a group (create_group names it with group; update_group); admin true on create_group makes an organization administrator group.
	IdentityProviderGroups []string          `json:"identity_provider_groups"`
	NetworkRoles           map[string]string `json:"network_roles"`
	// Confirm must equal the username or group name for what cannot be undone or widens access to everything.
	Confirm string `json:"confirm"`
	Apply   bool   `json:"apply"`
}

// editAccess manages users and access control groups: create, disable, delete, grant or revoke organization admin, set a network role for a user or a group, and define groups.
// Dry run unless apply; before, after and undo are recorded; an organization administrator is needed (MANAGE_USER_ACCOUNTS), and what Forward would refuse is refused here first
// with the reason. It never reads or prints a password back.
func editAccess(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editAccessInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, Scope: "account", State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	me, err := session(ctx, s)
	if err != nil {
		return accessError(err, cx, "read this login's roles before changing access")
	}
	refuse := func(msg string, limits ...string) (result.Result, error) {
		return result.Build(editAccessName, result.Failed, "Refused, nothing was changed: "+msg, result.Deterministic, cx, result.Options{Mode: mode,
			Limits:   append([]string{"nothing was sent to Forward"}, limits...),
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "access", nil, map[string]any{"action": in.Action, "mode": mode}, "")}, NextActions: []string{"inspect-access"}})
	}
	callerAdmin := me.Roles.HasOrgAdmin()
	workspaceGroupRole := in.Action == "set_network_role" && in.Group != "" && in.Role != "" && in.Role != "none"
	if !callerAdmin && !workspaceGroupRole {
		model := knowledge.RBACModel()
		need := "MANAGE_USER_ACCOUNTS, which only an organization administrator holds"
		if model == nil {
			need += " (this build carries no role model for more detail)"
		}
		return result.NewUnknown(editAccessName, "This login cannot manage users or access: it needs an organization administrator", cx,
			[]string{"needs OrgOperation." + need, "resolution: ask an organization administrator to make this change or to make you one; a network ADMIN cannot manage users",
				"the one exception is granting a group a role on a WORKSPACE network, which needs ASSIGN_ROLES there (WORKSPACE_OPERATOR or higher) and a role no higher than yours",
				"nothing was changed"}, result.Options{NextActions: []string{"inspect-access"}})
	}
	var plan *accessPlan
	switch in.Action {
	case "create_user", "set_user", "delete_user", "reset_2fa", "set_org_admin":
		plan, err = planUserAction(ctx, s, me, in)
	case "set_network_role":
		plan, err = planNetworkRole(ctx, s, me, in, callerAdmin)
	case "create_group", "update_group", "delete_group":
		plan, err = planGroupAction(ctx, s, in)
	default:
		return result.Result{}, fmt.Errorf("%w: action must be create_user, set_user, delete_user, reset_2fa, set_org_admin, set_network_role, create_group, update_group or delete_group", ErrInvalidInput)
	}
	if err != nil {
		return result.Result{}, err
	}
	if plan.refuse != "" {
		return refuse(plan.refuse, plan.limits...)
	}
	if plan.noop != "" {
		return result.Build(editAccessName, result.OK, plan.noop, result.Deterministic, cx, result.Options{Mode: mode, Limits: plan.limits,
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "access", nil, map[string]any{"action": in.Action, "before": plan.before}, "")}})
	}
	ch := result.Change{Action: in.Action, Target: plan.target, Before: plan.before, After: plan.after, Reversible: plan.undo != "", Undo: plan.undo}
	if ch.Undo == "" {
		ch.Undo = plan.noUndo
	}
	limits := append(plan.limits, "access changes take effect at once for the people concerned; their open sessions keep the old roles only until they next sign in")
	ev := []result.Evidence{result.NewEvidence(result.EvState, "access", nil, map[string]any{"action": in.Action, "target": plan.target, "before": plan.before, "after": plan.after, "needs_confirm": plan.confirm != ""}, "")}
	if !in.Apply {
		msg := fmt.Sprintf("Dry run: would %s. Nothing was changed; run again with apply=true", plan.describe)
		if plan.confirm != "" {
			msg += fmt.Sprintf(" and confirm=%q", plan.confirm)
		}
		return result.Build(editAccessName, result.OK, msg+". Undo: "+ch.Undo, result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: ev, NextActions: []string{"inspect-access"}})
	}
	if plan.confirm != "" && !strings.EqualFold(strings.TrimSpace(in.Confirm), plan.confirm) {
		return result.Build(editAccessName, result.Failed, fmt.Sprintf("Refused, nothing was changed: %s needs confirm=%q", plan.describe, plan.confirm), result.Deterministic, cx,
			result.Options{Mode: mode, Limits: limits, Evidence: ev, NextActions: []string{"inspect-access"}})
	}
	if err := plan.apply(); err != nil {
		return result.Result{}, fmt.Errorf("the change failed and nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	if plan.verify != nil {
		if msg := plan.verify(); msg != "" {
			return result.Build(editAccessName, result.Failed, fmt.Sprintf("Forward accepted the change but reading it back shows %s. Undo: %s", msg, ch.Undo), result.Deterministic, cx,
				result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: ev})
		}
	}
	return result.Build(editAccessName, result.OK, fmt.Sprintf("Applied: %s (read back). Undo: %s", plan.describe, ch.Undo), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: ev, NextActions: []string{"inspect-access"}})
}

// accessPlan is one change worked out and checked before anything is sent.
type accessPlan struct {
	target, describe, undo, noUndo, confirm, refuse, noop string
	before, after                                         any
	limits                                                []string
	apply                                                 func() error
	verify                                                func() string // "" when the read-back matches
}

func isSSO(source string) bool { s := strings.ToUpper(source); return s == "SAML" || s == "LDAP" }

func findUser(ctx context.Context, s *fwd.Session, q string) (*forward.UserWithRoles, error) {
	users, _, err := s.Client.Users.ListWithRoles(ctx)
	if err != nil {
		return nil, err
	}
	var hit []*forward.UserWithRoles
	for i := range users {
		u := &users[i]
		if strings.EqualFold(string(u.ID), q) || strings.EqualFold(u.Username, q) || strings.EqualFold(u.Email, q) {
			hit = append(hit, u)
		}
	}
	if len(hit) == 1 {
		return hit[0], nil
	}
	if len(hit) > 1 {
		return nil, fmt.Errorf("%w: %q matches %d users; give the id", ErrInvalidInput, q, len(hit))
	}
	return nil, nil
}

func planUserAction(ctx context.Context, s *fwd.Session, me *forward.UserSession, in editAccessInput) (*accessPlan, error) {
	if in.Action == "create_user" {
		if !strings.Contains(in.User, "@") || in.Password == "" {
			return nil, fmt.Errorf("%w: create_user needs user (the new user's email, which is also the username) and password (a temporary one: the user changes it at first sign-in)", ErrInvalidInput)
		}
		uname := in.User
		if u, err := findUser(ctx, s, uname); err != nil {
			return nil, err
		} else if u != nil {
			return &accessPlan{refuse: fmt.Sprintf("a user %q already exists", uname)}, nil
		}
		return &accessPlan{target: "user " + uname, describe: fmt.Sprintf("create the local user %s (password not shown)", uname), before: nil,
			after: map[string]any{"username": uname, "enabled": in.Enabled == nil || *in.Enabled, "roles": "none: grant one with set_network_role or set_org_admin"},
			undo:  "delete_user " + uname + " (with confirm)", limits: []string{"creates a LOCAL user only: single sign-on users are created by their first sign-in and get roles from their groups", "the password is never read back or shown"},
			apply: func() error {
				_, _, err := s.Client.Users.Create(ctx, forward.UserCreateRequest{Email: in.User, Password: in.Password, Enabled: in.Enabled})
				return err
			},
			verify: func() string {
				if u, err := findUser(ctx, s, uname); err != nil || u == nil {
					return "no such user"
				}
				return ""
			}}, nil
	}
	if strings.TrimSpace(in.User) == "" {
		return nil, fmt.Errorf("%w: %s needs user (id, username or email)", ErrInvalidInput, in.Action)
	}
	u, err := findUser(ctx, s, in.User)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return &accessPlan{refuse: fmt.Sprintf("no user matches %q (users are matched exactly by id, username or email; inspect-access view users lists them)", in.User)}, nil
	}
	self := string(u.ID) == string(me.User.ID)
	id := string(u.ID)
	state := map[string]any{"username": u.Username, "enabled": u.Enabled, "org_admin": u.OrgAdmin, "auth_source": u.AuthSource, "networks": len(u.AccessibleNetworks)}
	p := &accessPlan{target: "user " + u.Username, before: state}
	switch in.Action {
	case "set_user":
		patch := forward.UserPatch{}
		after := map[string]any{}
		for k, v := range state {
			after[k] = v
		}
		if in.Enabled != nil && *in.Enabled != u.Enabled {
			if self && !*in.Enabled {
				return &accessPlan{refuse: "Forward does not let you disable your own account"}, nil
			}
			patch.Enabled, after["enabled"] = in.Enabled, *in.Enabled
		}
		if patch.Enabled == nil {
			return &accessPlan{noop: fmt.Sprintf("%s already has those settings; nothing to change", u.Username), before: state}, nil
		}
		p.describe, p.after = fmt.Sprintf("change %s: %s", u.Username, changed(state, after)), after
		p.undo = "set_user " + u.Username + " back to " + changed(after, state)
		if patch.Enabled != nil && !*patch.Enabled && u.OrgAdmin {
			p.confirm = u.Username
			p.limits = append(p.limits, "this user is an organization administrator: disabling ends their sessions")
		}
		p.apply = func() error { _, _, err := s.Client.Users.Patch(ctx, id, patch); return err }
		p.verify = func() string {
			n, err := findUser(ctx, s, id)
			if err != nil || n == nil {
				return "the user could not be read back"
			}
			if patch.Enabled != nil && n.Enabled != *patch.Enabled {
				return fmt.Sprintf("enabled=%v", n.Enabled)
			}
			return ""
		}
	case "delete_user":
		if self {
			return &accessPlan{refuse: "Forward does not let you delete your own account"}, nil
		}
		p.describe, p.confirm = fmt.Sprintf("delete the user %s and every role and API token they hold", u.Username), u.Username
		var nets []map[string]string
		for _, n := range u.AccessibleNetworks {
			nets = append(nets, map[string]string{"id": string(n.ID), "role": n.Role})
		}
		state["network_roles"] = nets
		p.noUndo = fmt.Sprintf("cannot be undone: the user, their tokens and password are gone. Recreate with create_user and re-grant the roles in before (%d networks, org_admin=%v)", len(u.AccessibleNetworks), u.OrgAdmin)
		if isSSO(u.AuthSource) {
			p.limits = append(p.limits, fmt.Sprintf("%s signs in through %s: the account is recreated at their next sign-in with the roles their groups grant", u.Username, u.AuthSource))
		}
		p.apply = func() error { _, err := s.Client.Users.Delete(ctx, id); return err }
		p.verify = func() string {
			if n, err := findUser(ctx, s, id); err == nil && n != nil {
				return "the user still exists"
			}
			return ""
		}
	case "reset_2fa":
		p.describe, p.confirm, p.after = fmt.Sprintf("reset two-factor authentication for %s", u.Username), u.Username, map[string]any{"two_factor": "not set up"}
		p.noUndo = "cannot be undone: the user must enrol a second factor again (their trusted devices are also cleared)"
		p.apply = func() error { _, err := s.Client.Users.Reset2FA(ctx, id); return err }
	case "set_org_admin":
		if in.Admin == nil {
			return nil, fmt.Errorf("%w: set_org_admin needs admin true or false", ErrInvalidInput)
		}
		if *in.Admin == u.OrgAdmin {
			return &accessPlan{noop: fmt.Sprintf("%s is already %s; nothing to change", u.Username, map[bool]string{true: "an organization administrator", false: "not an organization administrator"}[u.OrgAdmin]), before: state}, nil
		}
		p.confirm = u.Username
		if *in.Admin {
			p.describe, p.after = fmt.Sprintf("make %s an organization administrator (every operation on every network, including user management)", u.Username), map[string]any{"org_admin": true}
			p.undo = "set_org_admin " + u.Username + " admin=false (with confirm)"
			p.apply = func() error { _, err := s.Client.Users.GrantOrgAdmin(ctx, id); return err }
		} else {
			if self {
				return &accessPlan{refuse: "Forward does not let you revoke your own organization admin role; ask another administrator"}, nil
			}
			p.describe, p.after = fmt.Sprintf("remove organization administrator from %s", u.Username), map[string]any{"org_admin": false}
			p.undo = "set_org_admin " + u.Username + " admin=true (with confirm)"
			p.limits = append(p.limits, "a user can still be an administrator through an access control group whose identity-provider group they belong to: revoking removes only the direct role")
			p.apply = func() error { _, err := s.Client.Users.RevokeOrgAdmin(ctx, id); return err }
		}
		want := *in.Admin
		p.verify = func() string {
			n, err := findUser(ctx, s, id)
			if err != nil || n == nil {
				return "the user could not be read back"
			}
			if n.OrgAdmin != want && !(!want && n.OrgAdmin) {
				return fmt.Sprintf("org_admin=%v", n.OrgAdmin)
			}
			return ""
		}
	}
	return p, nil
}

// changed describes the fields in b that differ from a.
func changed(a, b map[string]any) string {
	var out []string
	for k, v := range b {
		if fmt.Sprint(a[k]) != fmt.Sprint(v) {
			out = append(out, fmt.Sprintf("%s=%v", k, v))
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

var networkRoles = map[string]bool{"LIMITED_READ_ONLY": true, "READ_ONLY": true, "OPERATOR": true, "WORKSPACE_OPERATOR": true, "ADMIN": true}

func planNetworkRole(ctx context.Context, s *fwd.Session, me *forward.UserSession, in editAccessInput, callerAdmin bool) (*accessPlan, error) {
	role := strings.ToUpper(strings.TrimSpace(in.Role))
	if in.NetworkID == "" || role == "" || (in.User == "") == (in.Group == "") {
		return nil, fmt.Errorf("%w: set_network_role needs network_id, role (a network role or none) and exactly one of user or group", ErrInvalidInput)
	}
	if role != "NONE" && !networkRoles[role] {
		return &accessPlan{refuse: fmt.Sprintf("%q is not a network role; the roles are LIMITED_READ_ONLY, READ_ONLY, OPERATOR, WORKSPACE_OPERATOR, ADMIN, or none", in.Role)}, nil
	}
	lim := []string{"WORKSPACE_OPERATOR is valid only on a workspace network; Forward refuses it elsewhere"}
	if in.User != "" {
		u, err := findUser(ctx, s, in.User)
		if err != nil {
			return nil, err
		}
		if u == nil {
			return &accessPlan{refuse: fmt.Sprintf("no user matches %q", in.User)}, nil
		}
		cur := ""
		for _, n := range u.AccessibleNetworks {
			if string(n.ID) == in.NetworkID {
				cur = n.Role
			}
		}
		if u.OrgAdmin {
			return &accessPlan{refuse: fmt.Sprintf("%s is an organization administrator and already holds every role on every network", u.Username)}, nil
		}
		if strings.EqualFold(cur, role) || (cur == "" && role == "NONE") {
			return &accessPlan{noop: fmt.Sprintf("%s already has %s on network %s; nothing to change", u.Username, roleOrNone(cur), in.NetworkID)}, nil
		}
		if string(u.ID) == string(me.User.ID) {
			return &accessPlan{refuse: "change your own network role through another administrator: a role you lower on yourself can lock you out of the network"}, nil
		}
		if isSSO(u.AuthSource) {
			lim = append(lim, fmt.Sprintf("%s signs in through %s: a direct role works but their identity-provider groups also grant roles through access control groups, and the higher applies; prefer changing the group", u.Username, u.AuthSource))
		}
		id := string(u.ID)
		p := &accessPlan{target: fmt.Sprintf("%s on network %s", u.Username, in.NetworkID), before: map[string]any{"role": roleOrNone(cur)}, after: map[string]any{"role": strings.ToLower(role)},
			describe: fmt.Sprintf("set %s to %s on network %s", u.Username, role, in.NetworkID), limits: lim}
		if cur == "" {
			p.undo = fmt.Sprintf("set_network_role %s on network %s to none", u.Username, in.NetworkID)
		} else {
			p.undo = fmt.Sprintf("set_network_role %s on network %s back to %s", u.Username, in.NetworkID, cur)
		}
		if role == "NONE" {
			p.apply = func() error { _, err := s.Client.Users.ClearNetworkRoles(ctx, id, in.NetworkID); return err }
		} else {
			p.apply = func() error {
				_, err := s.Client.Users.SetNetworkRole(ctx, id, in.NetworkID, forward.NetworkRole(role))
				return err
			}
		}
		p.verify = func() string {
			n, err := findUser(ctx, s, id)
			if err != nil || n == nil {
				return "the user could not be read back"
			}
			got := ""
			for _, nn := range n.AccessibleNetworks {
				if string(nn.ID) == in.NetworkID {
					got = nn.Role
				}
			}
			if (role == "NONE" && got != "") || (role != "NONE" && !strings.EqualFold(got, role)) {
				return "role " + roleOrNone(got)
			}
			return ""
		}
		return p, nil
	}
	g, err := findGroup(ctx, s, in.Group)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return &accessPlan{refuse: fmt.Sprintf("no access control group matches %q", in.Group)}, nil
	}
	if g.IsOrgAdmin() {
		return &accessPlan{refuse: fmt.Sprintf("%s is an organization administrator group: it holds every role everywhere", g.Name)}, nil
	}
	cur := g.NetworkRoles[in.NetworkID]
	if strings.EqualFold(cur, role) || (cur == "" && role == "NONE") {
		return &accessPlan{noop: fmt.Sprintf("group %s already has %s on network %s; nothing to change", g.Name, roleOrNone(cur), in.NetworkID)}, nil
	}
	gid := string(g.ID)
	p := &accessPlan{target: fmt.Sprintf("group %s on network %s", g.Name, in.NetworkID), before: map[string]any{"role": roleOrNone(cur), "members": "everyone whose identity-provider groups match: " + strings.Join(g.ExternalGroupNames, ", ")},
		after: map[string]any{"role": strings.ToLower(role)}, describe: fmt.Sprintf("set group %s to %s on network %s", g.Name, role, in.NetworkID), limits: lim}
	p.limits = append(p.limits, "this changes access for every user in the group")
	if cur == "" {
		p.undo = fmt.Sprintf("set_network_role group %s on network %s to none", g.Name, in.NetworkID)
	} else {
		p.undo = fmt.Sprintf("set_network_role group %s on network %s back to %s", g.Name, in.NetworkID, cur)
	}
	if callerAdmin {
		roles := map[string]string{}
		for k, v := range g.NetworkRoles {
			roles[k] = v
		}
		if role == "NONE" {
			delete(roles, in.NetworkID)
		} else {
			roles[in.NetworkID] = role
		}
		p.apply = func() error {
			_, _, err := s.Client.AccessControl.UpdateGroup(ctx, gid, forward.AccessControlGroupRequest{Name: g.Name, ExternalGroupNames: g.ExternalGroupNames, NetworkRoles: roles, DeviceAccessLabelIDs: g.DeviceAccessLabelIDs})
			return err
		}
	} else {
		if role == "NONE" {
			return &accessPlan{refuse: "removing a group's role needs an organization administrator; ASSIGN_ROLES on a workspace only adds or changes a role up to your own"}, nil
		}
		p.limits = append(p.limits, "done as ASSIGN_ROLES on a workspace network: Forward refuses a non-workspace network and a role above your own")
		p.apply = func() error {
			_, _, err := s.Client.AccessControl.SetGroupNetworkRole(ctx, gid, in.NetworkID, forward.NetworkRole(role))
			return err
		}
	}
	p.verify = func() string {
		n, err := findGroup(ctx, s, gid)
		if err != nil || n == nil {
			return "the group could not be read back"
		}
		got := n.NetworkRoles[in.NetworkID]
		if (role == "NONE" && got != "") || (role != "NONE" && !strings.EqualFold(got, role)) {
			return "role " + roleOrNone(got)
		}
		return ""
	}
	return p, nil
}

func roleOrNone(s string) string {
	if s == "" {
		return "no role"
	}
	return s
}

func findGroup(ctx context.Context, s *fwd.Session, q string) (*forward.AccessControlGroup, error) {
	gs, _, err := s.Client.AccessControl.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range gs {
		if strings.EqualFold(string(gs[i].ID), q) || strings.EqualFold(gs[i].Name, q) {
			return &gs[i], nil
		}
	}
	return nil, nil
}

func planGroupAction(ctx context.Context, s *fwd.Session, in editAccessInput) (*accessPlan, error) {
	for k, v := range in.NetworkRoles {
		if !networkRoles[strings.ToUpper(v)] {
			return &accessPlan{refuse: fmt.Sprintf("network_roles[%s]=%q is not a network role", k, v)}, nil
		}
	}
	lim := []string{"a group has no member list: a user is in it when one of their identity-provider group names matches; only SAML and LDAP users use groups"}
	if in.Action == "create_group" {
		if strings.TrimSpace(in.Group) == "" {
			return nil, fmt.Errorf("%w: create_group needs group (the new name)", ErrInvalidInput)
		}
		name := strings.TrimSpace(in.Group)
		if g, err := findGroup(ctx, s, name); err != nil {
			return nil, err
		} else if g != nil {
			return &accessPlan{refuse: fmt.Sprintf("a group named %q already exists (names are unique, ignoring case)", name)}, nil
		}
		req := forward.AccessControlGroupRequest{Name: name, ExternalGroupNames: in.IdentityProviderGroups, NetworkRoles: in.NetworkRoles}
		orgAdmin := in.Admin != nil && *in.Admin
		if orgAdmin {
			if len(in.NetworkRoles) > 0 {
				return &accessPlan{refuse: "an organization administrator group carries no network roles"}, nil
			}
			req = forward.OrgAdminGroup(name, in.IdentityProviderGroups)
		} else if len(in.NetworkRoles) == 0 {
			req.NetworkRoles = map[string]string{}
		}
		p := &accessPlan{target: "group " + name, describe: fmt.Sprintf("create the access control group %s", name), before: nil,
			after: map[string]any{"identity_provider_groups": in.IdentityProviderGroups, "network_roles": in.NetworkRoles, "org_admin": orgAdmin},
			undo:  "delete_group " + name + " (with confirm)", limits: lim}
		if orgAdmin {
			p.confirm = name
			p.limits = append(p.limits, "organization administrator group: everyone in the identity-provider groups listed becomes an administrator")
		}
		p.apply = func() error { _, _, err := s.Client.AccessControl.CreateGroup(ctx, req); return err }
		p.verify = func() string {
			if g, err := findGroup(ctx, s, name); err != nil || g == nil {
				return "no such group"
			}
			return ""
		}
		return p, nil
	}
	if strings.TrimSpace(in.Group) == "" {
		return nil, fmt.Errorf("%w: %s needs group (id or name)", ErrInvalidInput, in.Action)
	}
	g, err := findGroup(ctx, s, in.Group)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return &accessPlan{refuse: fmt.Sprintf("no access control group matches %q", in.Group)}, nil
	}
	gid := string(g.ID)
	before := map[string]any{"name": g.Name, "identity_provider_groups": g.ExternalGroupNames, "network_roles": g.NetworkRoles, "org_admin": g.IsOrgAdmin(), "device_access_labels": g.DeviceAccessLabelIDs}
	if in.Action == "delete_group" {
		return &accessPlan{target: "group " + g.Name, describe: fmt.Sprintf("delete the group %s (everyone in it loses the access it gives)", g.Name), before: before, confirm: g.Name,
			noUndo: "undo is create_group with the settings in before; users regain access at their next sign-in", limits: append(lim, "a user keeps any role assigned to them directly or through another group"),
			apply: func() error { _, err := s.Client.AccessControl.DeleteGroup(ctx, gid); return err },
			verify: func() string {
				if n, err := findGroup(ctx, s, gid); err == nil && n != nil {
					return "the group still exists"
				}
				return ""
			}}, nil
	}
	// update_group: replace the fields given, keep the rest
	req := forward.AccessControlGroupRequest{Name: g.Name, ExternalGroupNames: g.ExternalGroupNames, NetworkRoles: g.NetworkRoles, DeviceAccessLabelIDs: g.DeviceAccessLabelIDs}
	if in.IdentityProviderGroups != nil {
		req.ExternalGroupNames = in.IdentityProviderGroups
	}
	if in.NetworkRoles != nil {
		if g.IsOrgAdmin() {
			return &accessPlan{refuse: "an organization administrator group carries no network roles"}, nil
		}
		req.NetworkRoles = in.NetworkRoles
	}
	after := map[string]any{"name": req.Name, "identity_provider_groups": req.ExternalGroupNames, "network_roles": req.NetworkRoles}
	if fmt.Sprint(after["name"], after["identity_provider_groups"], after["network_roles"]) == fmt.Sprint(before["name"], before["identity_provider_groups"], before["network_roles"]) {
		return &accessPlan{noop: fmt.Sprintf("group %s already has those settings; nothing to change", g.Name), before: before}, nil
	}
	return &accessPlan{target: "group " + g.Name, describe: fmt.Sprintf("update the group %s", g.Name), before: before, after: after, undo: "update_group " + g.Name + " with the values in before",
		limits: append(lim, "network_roles replaces the whole map of the group: roles not listed are removed", "this changes access for every user in the group"),
		apply:  func() error { _, _, err := s.Client.AccessControl.UpdateGroup(ctx, gid, req); return err },
		verify: func() string {
			if n, err := findGroup(ctx, s, gid); err != nil || n == nil {
				return "the group could not be read back"
			}
			return ""
		}}, nil
}
