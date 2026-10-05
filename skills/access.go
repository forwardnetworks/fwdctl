package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
)

const inspectAccessName = "inspect-access"

func init() { Register(inspectAccessName, inspectAccess) }

type inspectAccessInput struct {
	// View is me (default), explain, users or groups.
	View      string `json:"view"`
	NetworkID string `json:"network_id"`
	// Operation (EDIT_CHECKS, NetworkOperation.EDIT_CHECKS) or Error (Forward's 403 text) names what was refused, for view explain.
	Operation string `json:"operation"`
	Error     string `json:"error"`
	// User narrows view users (or activity) to one user (id, username or email); Match narrows users or groups by name, or activity by route prefix.
	User   string `json:"user"`
	Match  string `json:"match"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	// Since (a span such as 7d, or an RFC 3339 time) and Method narrow view activity.
	Since  string `json:"since"`
	Until  string `json:"until"`
	Method string `json:"method"`
	Status string `json:"status"`
}

// inspectAccess answers "what can this login do, why was something refused, who has access". It reads: Forward's session (the roles in effect for this login, ACG grants and
// workspace inheritance included), the role model Forward's source defines, users and access control groups. It changes nothing.
func inspectAccess(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectAccessInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, Scope: "account", State: "current"}
	switch in.View {
	case "", "me":
		return accessMe(ctx, s, in, cx)
	case "explain":
		return accessExplain(ctx, s, in, cx)
	case "users":
		return accessUsers(ctx, s, in, cx)
	case "groups":
		return accessGroups(ctx, s, in, cx)
	case "activity":
		return accessActivity(ctx, s, in, cx)
	}
	return result.Result{}, fmt.Errorf("%w: view must be me, explain, users, groups or activity", ErrInvalidInput)
}

var opText = regexp.MustCompile(`(?:(Org|Network)Operation\.|network operation )?\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)\b`)

// parseOperation finds the operation a denial names: scope "org" or "network" ("" when the wording does not say) and the name.
func parseOperation(text string) (scope, name string) {
	m := opText.FindStringSubmatch(text)
	if m == nil {
		return "", ""
	}
	return strings.ToLower(m[1]), m[2]
}

func roleIsAtLeast(model *knowledge.RBAC, role, min string) bool {
	return model.Rank(role) >= 0 && model.Rank(role) >= model.Rank(min)
}

// highestNetworkRole is the highest network role a session holds anywhere, and where.
func highestNetworkRole(model *knowledge.RBAC, roles map[string]string) (string, string) {
	best, at := "", ""
	for n, r := range roles {
		if model.Rank(r) > model.Rank(best) || (model.Rank(r) == model.Rank(best) && n < at) {
			best, at = r, n
		}
	}
	return best, at
}

func session(ctx context.Context, s *fwd.Session) (*forward.UserSession, error) {
	us, _, err := s.Client.Users.CurrentSession(ctx)
	return us, err
}

func accessMe(ctx context.Context, s *fwd.Session, in inspectAccessInput, cx result.Context) (result.Result, error) {
	us, err := session(ctx, s)
	if err != nil {
		return accessError(err, cx, "read this login's own session")
	}
	model := knowledge.RBACModel()
	orgAdmin := us.Roles.HasOrgAdmin()
	d := map[string]any{"user": us.User.Username, "auth_source": us.User.AuthSource, "enabled": us.User.Enabled, "org_roles": us.Roles.Org, "org_admin": orgAdmin,
		"network_roles": us.Roles.Network, "group_ids": us.GroupIDs}
	if len(us.Roles.System) > 0 {
		d["system_roles"] = us.Roles.System
	}
	if us.Impersonator != "" {
		d["impersonator"] = us.Impersonator
	}
	limits := []string{"these are the roles in effect for this login: access control group grants are merged in and a workspace inherits its parent's role when the org property WORKSPACE_NETWORK_ROLE_AUTO_PROPAGATION is on; `view users` shows what is assigned directly",
		"a role is a ceiling set by Forward; device access labels on a group can still narrow which devices a network role sees"}
	var finding string
	switch {
	case orgAdmin:
		finding = fmt.Sprintf("%s is an organization administrator: every operation on every network, including user and access management", us.User.Username)
	case len(us.Roles.Network) == 0:
		finding = fmt.Sprintf("%s holds no role on any network", us.User.Username)
	default:
		finding = fmt.Sprintf("%s holds a role on %d network(s)", us.User.Username, len(us.Roles.Network))
	}
	if model == nil {
		limits = append(limits, "this build carries no role model, so what each role lets you do is not shown; the roles above are Forward's own")
	} else if !orgAdmin {
		top, at := highestNetworkRole(model, us.Roles.Network)
		if top != "" {
			d["highest_network_role"] = map[string]any{"role": top, "network": at}
		}
		if in.NetworkID != "" {
			role := us.Roles.Network[in.NetworkID]
			if role == "" {
				d["on_network"] = map[string]any{"network": in.NetworkID, "role": nil, "meaning": "no role: the network is not visible and every operation on it is refused"}
			} else {
				ops := model.OpsAt(role, "network")
				sort.Strings(ops)
				var writes []string
				for _, n := range ops {
					if o, ok := model.Op("network", n); ok && o.Access == "write" {
						writes = append(writes, n)
					}
				}
				d["on_network"] = map[string]any{"network": in.NetworkID, "role": role, "operations": len(ops), "write_operations": writes,
					"cannot": "anything above this role; `explain` says what a refused operation needs"}
			}
		}
		d["role_ladder"] = model.Order
		d["ladder_note"] = "each role includes everything below it; WORKSPACE_OPERATOR applies to workspace networks only; ADMIN on a network is not an organization administrator and cannot manage users"
	}
	return result.Build(inspectAccessName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "currentSession", nil, d, finding)}, NextActions: []string{"edit-access"}})
}

// accessExplain says why an operation was refused and what resolves it, from the operation a 403 names and the roles this login holds.
func accessExplain(ctx context.Context, s *fwd.Session, in inspectAccessInput, cx result.Context) (result.Result, error) {
	text := strings.TrimSpace(in.Error + " " + in.Operation)
	if strings.TrimSpace(text) == "" {
		return result.Result{}, fmt.Errorf("%w: view explain needs operation (EDIT_CHECKS) or error (Forward's 403 text)", ErrInvalidInput)
	}
	if strings.Contains(strings.ToLower(text), "unlicensed operation") {
		return result.Build(inspectAccessName, result.OK, "This is a licence problem, not a role: the organization's licence does not include that operation", result.Deterministic, cx,
			result.Options{Limits: []string{"no role grants an operation the licence lacks; the resolution is with the organization's Forward licence (account team or support)"},
				Evidence: []result.Evidence{result.NewEvidence(result.EvState, "explainDenial", nil, map[string]any{"cause": "unlicensed", "denial": text}, "")}})
	}
	scope, name := parseOperation(text)
	if name == "" {
		return result.NewUnknown(inspectAccessName, "No operation name could be read from that text", cx,
			[]string{"give operation (for example EDIT_CHECKS) or the full 403 message ('Missing permission: NetworkOperation.EDIT_CHECKS')"}, result.Options{})
	}
	model := knowledge.RBACModel()
	if model == nil {
		return result.NewUnknown(inspectAccessName, fmt.Sprintf("%s was refused, but this build carries no role model to say which role it needs", name), cx,
			[]string{"the role model is in the release binaries only (`fwdctl update`); install one to explain a refusal"}, result.Options{})
	}
	op, ok := model.Op(scope, name)
	if !ok {
		return result.NewUnknown(inspectAccessName, fmt.Sprintf("%s is not an operation this build knows (it may be newer than the role model)", name), cx,
			[]string{"the role model comes from Forward's source at build time; ask an organization administrator what the operation needs"}, result.Options{})
	}
	us, err := session(ctx, s)
	if err != nil {
		return accessError(err, cx, "read this login's roles to compare")
	}
	held := map[string]any{"org_roles": us.Roles.Org, "network_roles": us.Roles.Network}
	d := map[string]any{"operation": op.Scope + " operation " + op.Name, "meaning": op.Doc, "access": op.Access, "needs": needsText(op), "held": held}
	var finding string
	var fix []string
	orgAdmin := us.Roles.HasOrgAdmin()
	switch {
	case orgAdmin:
		finding = fmt.Sprintf("%s is refused although this login is an organization administrator", op.Name)
		fix = append(fix, "an organization administrator holds every operation, so the refusal is not the role: check the licence, a device access label (collected files), whether the network is a workspace (WORKSPACE_OPERATOR), the org property that gates the route, or on SaaS an operation only Forward staff hold (ADMINISTER_SYSTEM)")
	case op.MinRole == "ORG_ADMIN":
		finding = fmt.Sprintf("%s needs an organization administrator; no network role, including network ADMIN, grants it", op.Name)
		fix = append(fix, "ask an organization administrator to do it, or to make you one (edit-access grants org admin; it needs MANAGE_USER_ACCOUNTS)")
	case op.Scope == "network":
		if in.NetworkID == "" {
			top, at := highestNetworkRole(model, us.Roles.Network)
			finding = fmt.Sprintf("%s needs network role %s or higher on the network it was run against; give network_id to compare", op.Name, op.MinRole)
			d["highest_network_role"] = map[string]any{"role": top, "network": at}
			var can []string
			for n, r := range us.Roles.Network {
				if roleIsAtLeast(model, r, op.MinRole) {
					can = append(can, n)
				}
			}
			sort.Strings(can)
			d["networks_where_allowed"] = can
		} else if r := us.Roles.Network[in.NetworkID]; r == "" {
			finding = fmt.Sprintf("%s was refused: this login holds no role on network %s, and it needs %s or higher", op.Name, in.NetworkID, op.MinRole)
			fix = append(fix, fmt.Sprintf("an organization administrator can grant %s on network %s (edit-access)", op.MinRole, in.NetworkID))
		} else if !roleIsAtLeast(model, r, op.MinRole) {
			finding = fmt.Sprintf("%s was refused: this login is %s on network %s and the operation needs %s or higher", op.Name, r, in.NetworkID, op.MinRole)
			fix = append(fix, fmt.Sprintf("an organization administrator can raise the role to %s on network %s (edit-access); if the network is a workspace, anyone with ASSIGN_ROLES there (WORKSPACE_OPERATOR or higher) can grant a group a role up to their own", op.MinRole, in.NetworkID))
		} else {
			finding = fmt.Sprintf("%s is refused although this login is %s on network %s, which should hold it", op.Name, r, in.NetworkID)
			fix = append(fix, "the role is enough, so look elsewhere: a licence facet, a device access label narrowing devices, the network being a workspace or not (WORKSPACE_OPERATOR only works on workspaces), or a gating org property")
		}
	default: // an org operation a network role grants
		top, _ := highestNetworkRole(model, us.Roles.Network)
		if top != "" && roleIsAtLeast(model, top, op.MinRole) {
			finding = fmt.Sprintf("%s is refused although the highest role held (%s) should grant it", op.Name, top)
			fix = append(fix, "check the licence or a gating org property")
		} else {
			finding = fmt.Sprintf("%s needs a network role of %s or higher on any network; the highest held is %s", op.Name, op.MinRole, orNone(top))
			fix = append(fix, fmt.Sprintf("an organization administrator can grant %s on a network (edit-access)", op.MinRole))
		}
	}
	d["resolution"] = fix
	limits := []string{"the role model is Forward's own definition at build time; device access labels, licence facets and org properties can refuse an operation a role holds"}
	if len(fix) == 0 {
		limits = append(limits, "no resolution is offered until network_id says which network was refused")
	}
	return result.Build(inspectAccessName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "explainDenial", nil, d, finding)}, NextActions: []string{"edit-access"}})
}

func needsText(op *knowledge.RBACOp) string {
	if op.MinRole == "ORG_ADMIN" {
		return "organization administrator"
	}
	if op.Scope == "org" {
		return "network role " + op.MinRole + " or higher on any network (or organization administrator)"
	}
	return "network role " + op.MinRole + " or higher on the network (or organization administrator)"
}

func accessUsers(ctx context.Context, s *fwd.Session, in inspectAccessInput, cx result.Context) (result.Result, error) {
	users, _, err := s.Client.Users.ListWithRoles(ctx)
	if err != nil {
		return accessError(err, cx, "list users with their roles")
	}
	names, _, _ := s.Client.AccessControl.GroupNames(ctx)
	tfa, _, tfaErr := s.Client.Users.TwoFactorStatuses(ctx)
	m := strings.ToLower(strings.TrimSpace(in.Match + in.User))
	var rows []map[string]any
	for _, u := range users {
		if m != "" && !strings.Contains(strings.ToLower(u.Username+" "+u.Email+" "+string(u.ID)), m) {
			continue
		}
		var groups []string
		for _, g := range u.GroupIDs {
			groups = append(groups, firstNonEmpty(names[g], g))
		}
		row := map[string]any{"id": string(u.ID), "username": u.Username, "email": u.Email, "enabled": u.Enabled, "auth_source": u.AuthSource, "org_admin": u.OrgAdmin,
			"networks": len(u.AccessibleNetworks), "groups": groups, "last_active": u.LastActive}
		if u.APITokenLastUsedAt != "" {
			row["api_token_last_used"] = u.APITokenLastUsedAt
		}
		if t, ok := tfa[string(u.ID)]; ok {
			row["two_factor"] = t.SetUp
		}
		if in.User != "" {
			var nets []map[string]string
			for _, n := range u.AccessibleNetworks {
				nets = append(nets, map[string]string{"id": string(n.ID), "name": n.Name, "role": n.Role})
			}
			row["accessible_networks"] = nets
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return result.NewUnknown(inspectAccessName, "No user matches", cx, []string{fmt.Sprintf("%d users were read; none matched %q", len(users), m)}, result.Options{})
	}
	win, omitted, ok := window(rows, in.Limit, in.Offset, 50, 200, "users")
	if !ok {
		return result.NewUnknown(inspectAccessName, fmt.Sprintf("Offset %d is beyond the %d users", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	var limits []string
	if tfaErr != nil {
		limits = append(limits, "two-factor status could not be read: "+tfaErr.Error())
	}
	limits = append(limits, "roles here come from direct assignment and access control groups; single sign-on users (SAML, LDAP) get their roles from their identity-provider groups through the groups listed, and local users from direct assignment")
	admins := 0
	for _, r := range rows {
		if r["org_admin"] == true {
			admins++
		}
	}
	finding := fmt.Sprintf("%d users (%d organization administrators)", len(rows), admins)
	return result.Build(inspectAccessName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "listUsers", nil, map[string]any{"total": len(rows), "offset": in.Offset, "users": win}, finding)}, NextActions: []string{"edit-access"}})
}

func accessGroups(ctx context.Context, s *fwd.Session, in inspectAccessInput, cx result.Context) (result.Result, error) {
	groups, _, err := s.Client.AccessControl.ListGroups(ctx)
	if err != nil {
		return accessError(err, cx, "list access control groups")
	}
	labels, _, _ := s.Client.AccessControl.DeviceAccessLabelNames(ctx)
	m := strings.ToLower(strings.TrimSpace(in.Match))
	var rows []map[string]any
	for _, g := range groups {
		if m != "" && !strings.Contains(strings.ToLower(g.Name), m) {
			continue
		}
		var lab []string
		for _, l := range g.DeviceAccessLabelIDs {
			lab = append(lab, firstNonEmpty(labels[l], l))
		}
		rows = append(rows, map[string]any{"id": string(g.ID), "name": g.Name, "org_admin": g.IsOrgAdmin(), "identity_provider_groups": g.ExternalGroupNames,
			"network_roles": g.NetworkRoles, "device_access_labels": lab})
	}
	if len(rows) == 0 {
		return result.NewUnknown(inspectAccessName, "No access control group matches", cx, []string{fmt.Sprintf("%d groups were read", len(groups))}, result.Options{})
	}
	win, omitted, ok := window(rows, in.Limit, in.Offset, 50, 200, "groups")
	if !ok {
		return result.NewUnknown(inspectAccessName, fmt.Sprintf("Offset %d is beyond the %d groups", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	var limits []string
	limits = append(limits, "a group has no member list: a user is in it when one of their identity-provider group names matches (ignoring case); only SAML and LDAP users use groups; roles from several groups and direct assignment combine to the highest per network")
	finding := fmt.Sprintf("%d access control groups", len(rows))
	return result.Build(inspectAccessName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "listAccessControlGroups", nil, map[string]any{"total": len(rows), "offset": in.Offset, "groups": win}, finding)}, NextActions: []string{"edit-access"}})
}

// accessError turns a permission denial into an answer: what was refused, what it needs, and who can resolve it. Any other error is returned as it is.
func accessError(err error, cx result.Context, doing string) (result.Result, error) {
	if r, ok := denialResult(inspectAccessName, err, cx, "could not "+doing); ok {
		return r, nil
	}
	return result.Result{}, err
}
