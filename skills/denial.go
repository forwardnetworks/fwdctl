package skills

import (
	"fmt"

	forward "github.com/forwardnetworks/forward-go-sdk"

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
	op, ok := forward.MissingPermission(err)
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
