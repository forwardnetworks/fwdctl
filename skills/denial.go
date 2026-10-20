package skills

import (
	"fmt"
	"regexp"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
)

// denialResult explains a Forward 403 (a missing permission or licence) as an unknown result naming the operation, the lowest role that holds it and who can resolve it.
// Nothing is known to have changed. Skill Run calls it for every skill so a refusal reads the same everywhere.
func denialResult(skill string, err error, cx result.Context, doing string) (result.Result, bool) {
	if op, ok := forward.UnlicensedOperation(err); ok {
		r, _ := result.NewUnknown(skill, fmt.Sprintf("Forward refused %s: the licence does not include %s", doing, op), cx,
			[]string{"this is a licence limit, not a role: no role grants it; nothing was changed", "resolution: the organization's Forward licence (account team or support)"},
			result.Options{NextActions: []string{"inspect-access"}})
		return r, true
	}
	if r, ok := wordedDenial(skill, err, cx, doing); ok {
		return r, true
	}
	op, ok := forward.MissingPermission(err)
	if !ok {
		// the other wording of a missing network permission: "No permission for network operation EDIT_CHECKS"
		if m := denialNetworkPermission.FindStringSubmatch(err.Error()); m != nil && (fwd.Status(err) == 403 || fwd.Status(err) == 401) {
			op, ok = "NetworkOperation."+m[1], true
		}
	}
	if !ok {
		return result.Result{}, false
	}
	scope, name := parseOperation(op)
	needs := "a role that holds it"
	limits := []string{"nothing was changed", "run inspect-access with view explain and this operation (and network_id) for the role it needs and who can resolve it"}
	if model := knowledge.RBACModel(); model != nil {
		if o, found := model.Op(scope, name); found {
			needs = needsText(o)
			limits = append(limits, fmt.Sprintf("%s: %s", o.Name, o.Doc))
		}
	}
	r, _ := result.NewUnknown(skill, fmt.Sprintf("Forward refused %s: this login lacks %s, which needs %s", doing, op, needs), cx, limits,
		result.Options{NextActions: []string{"inspect-access"}})
	return r, true
}

// The other wordings of a refusal. Forward sends no reason code on a 403 (it is null), so the message is the only signal; these are the access enforcer's own format strings as the Python SDK read them. The licence-expiry pattern matches only the
// tail of the sentence because the full text contains a typographic apostrophe. Unrecognised wording is not classified (the caller reports the plain refusal): a hint for an operator, never a branch that changes what happens.
var (
	denialOrgSetting        = regexp.MustCompile(`([A-Z][A-Z0-9_]+) is (?:on|off) for your organization`)
	denialDeploymentSetting = regexp.MustCompile(`([A-Z][A-Z0-9_]+) is (?:on|off) for your deployment`)
	denialAuthority         = regexp.MustCompile(`(?:^|:\s)([^:]+?)\s+authority required`)
	denialLicenseExpired    = regexp.MustCompile(`license has expired`)
	denialNetworkPermission = regexp.MustCompile(`No permission for network operation\s+([A-Z][A-Z0-9_]+)`)
)

func wordedDenial(skill string, err error, cx result.Context, doing string) (result.Result, bool) {
	if fwd.Status(err) != 403 && fwd.Status(err) != 401 {
		return result.Result{}, false
	}
	msg := err.Error()
	unknown := func(finding string, limits ...string) (result.Result, bool) {
		r, _ := result.NewUnknown(skill, finding, cx, append([]string{"nothing was changed"}, limits...), result.Options{NextActions: []string{"inspect-access"}})
		return r, true
	}
	switch {
	case denialLicenseExpired.MatchString(msg):
		return unknown(fmt.Sprintf("Forward refused %s: the organization's licence has expired", doing),
			"in the read-only grace period existing data can be read but nothing new is collected; after it only an organization administrator can sign in",
			"resolution: renew the licence (account team or support); no role or setting of this login changes it")
	case denialDeploymentSetting.MatchString(msg):
		name := denialDeploymentSetting.FindStringSubmatch(msg)[1]
		return unknown(fmt.Sprintf("Forward refused %s: %s is a deployment setting that is not enabled", doing, name),
			"a deployment setting is not changeable by an organization administrator or a role of this login",
			"resolution: whoever runs the Forward deployment (or Forward support for a hosted one); inspect-platform area org_properties shows the value and who may change it")
	case denialOrgSetting.MatchString(msg):
		name := denialOrgSetting.FindStringSubmatch(msg)[1]
		return unknown(fmt.Sprintf("Forward refused %s: the organization property %s is not enabled", doing, name),
			"this is a setting, not a role or a licence limit",
			"resolution: edit-org-property (dry run first); on a hosted Forward most Predict properties can only be changed by Forward support, with a support login (org_id and --support-token-file)")
	case denialAuthority.MatchString(msg):
		return unknown(fmt.Sprintf("Forward refused %s: it needs %s authority", doing, strings.TrimSpace(denialAuthority.FindStringSubmatch(msg)[1])),
			"this needs Forward support authority: a platform administrator login (Forward support or a Forward administrator), which an organization role does not give",
			"resolution: a support login for this call (--support-token-file), or ask Forward support")
	}
	return result.Result{}, false
}
