package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
)

const editOrgPropertyName = "edit-org-property"

func init() { Register(editOrgPropertyName, editOrgProperty) }

type editOrgPropertyInput struct {
	// Property is the organization property to change; omit it to list what is configurable and its current value.
	Property string `json:"property"`
	Value    string `json:"value"`
	// Clear removes the organization's override so the deployment default applies again.
	Clear bool `json:"clear"`
	// Confirm must equal the property name to apply a property classified dangerous or not classified at all.
	Confirm string `json:"confirm"`
	// Match narrows the list (a substring of the name or description); Limit and Offset page it.
	Match  string `json:"match"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Apply  bool   `json:"apply"`
}

// editOrgProperty lists the organization properties Forward defines with their current value and how risky each is to change, and sets (or clears) one for the login's own
// organization. A property is an ORGANIZATION-wide setting: it applies to every network and every user. The property names and values come from Forward itself (GET /api/config);
// what each property is, the values it accepts, who may change it and how dangerous it is come from Forward's source and a reviewed classification. Anything it cannot establish it
// says, and refuses: unknown is never a pass.
func editOrgProperty(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editOrgPropertyInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{Scope: "account", State: "current"}
	cfg := s.OrgConfig(ctx)
	if cfg.Effective == nil {
		return result.NewUnknown(editOrgPropertyName, "The organization's properties could not be read", cx,
			[]string{"the effective configuration read failed: " + cfg.Errs["effective"] + "; nothing was changed"}, result.Options{NextActions: []string{"inspect-environment"}})
	}
	if strings.TrimSpace(in.Property) == "" {
		if in.Value != "" || in.Clear || in.Confirm != "" || in.Apply {
			return result.Result{}, fmt.Errorf("%w: value, clear, confirm and apply belong to a run with a property", ErrInvalidInput)
		}
		return listOrgProperties(cfg, in, cx)
	}
	name := strings.ToLower(strings.TrimSpace(in.Property))
	cur, exists := cfg.Effective[name]
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	evd := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"property": strings.ToUpper(name), "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "orgConfig", nil, d, "")}
	}
	refuse := func(msg string, extra map[string]any) (result.Result, error) {
		return result.Build(editOrgPropertyName, result.Failed, "Refused, nothing was changed: "+msg, result.Deterministic, cx,
			result.Options{Mode: mode, Limits: []string{"nothing was sent to Forward"}, Evidence: evd(extra), NextActions: []string{"inspect-environment"}})
	}
	if !exists {
		return refuse(fmt.Sprintf("this Forward defines no organization property %q (the list is Forward's own; edit-org-property with no property lists them)", strings.ToUpper(name)), nil)
	}
	info, known := knowledge.OrgPropertyInfo(name)
	risk, reason, consequence := "unclassified", "this build has no reviewed classification for it: treat it as caution", ""
	admin := "unknown"
	if known {
		risk, reason, consequence, admin = info.Risk, info.Reason, info.Consequence, info.Admin
	}
	_, overridden := cfg.Configured[name]
	def := cfg.Global[name]
	before := map[string]any{"value": cur, "overridden": overridden, "default": def}
	facts := map[string]any{"risk": risk, "reason": reason, "consequence": consequence, "who_can_change": whoCanChange(admin), "before": before}
	if known && admin == "NONE" {
		return refuse(fmt.Sprintf("%s can be changed only by Forward support, not by an organization administrator (%s)", strings.ToUpper(name), whoCanChange(admin)), facts)
	}

	// what the change is
	var after any
	var undo string
	switch {
	case in.Clear:
		if in.Value != "" {
			return result.Result{}, fmt.Errorf("%w: give value or clear, not both", ErrInvalidInput)
		}
		if !overridden {
			return result.Build(editOrgPropertyName, result.OK, fmt.Sprintf("%s has no organization override (it is at the default %s); nothing to clear", strings.ToUpper(name), def), result.Deterministic, cx,
				result.Options{Mode: mode, Evidence: evd(facts)})
		}
		after = map[string]any{"overridden": false, "value": def}
		undo = fmt.Sprintf("set %s back to %s (value) with apply", strings.ToUpper(name), cfg.Configured[name])
	default:
		if strings.TrimSpace(in.Value) == "" {
			return result.Result{}, fmt.Errorf("%w: value is required (or clear: true)", ErrInvalidInput)
		}
		if msg := validOrgValue(info, known, cur, in.Value); msg != "" {
			return refuse(msg, facts)
		}
		if in.Value == cur && overridden {
			return result.Build(editOrgPropertyName, result.OK, fmt.Sprintf("%s already has the value %s as an organization override; nothing to change", strings.ToUpper(name), cur), result.Deterministic, cx,
				result.Options{Mode: mode, Evidence: evd(facts)})
		}
		after = map[string]any{"overridden": true, "value": in.Value}
		if overridden {
			undo = fmt.Sprintf("set %s back to %s with apply", strings.ToUpper(name), cfg.Configured[name])
		} else {
			undo = fmt.Sprintf("clear the organization override of %s (clear: true with apply): it had none, so the default %s applies again", strings.ToUpper(name), def)
		}
	}
	limits := []string{
		"a property is ORGANIZATION-wide: it applies to every network and every user of the organization",
		fmt.Sprintf("risk %s: %s", risk, reason),
	}
	if consequence != "" {
		limits = append(limits, "what it can break: "+consequence)
	}
	if known && hasKnownUse(info.Uses, "COMPUTATION") {
		limits = append(limits, "it affects how snapshots are COMPUTED: it applies to snapshots processed after the change; existing ones are unchanged until reprocessed")
	}
	if !known {
		limits = append(limits, "this build holds no definition of the property (it may be newer than the table): its kind, allowed values and who may change it were not checked")
	}
	if known && admin == "ON_PREMISES" {
		limits = append(limits, "organization administrators may change it only on an on-premises Forward: on SaaS Forward refuses (403)")
	}
	needConfirm := risk == "dangerous" || risk == "unclassified"
	confirmed := strings.EqualFold(strings.TrimSpace(in.Confirm), name)
	ch := result.Change{Action: map[bool]string{true: "clear_org_property", false: "set_org_property"}[in.Clear], Target: "organization property " + strings.ToUpper(name),
		Before: before, After: after, Reversible: true, Undo: undo}
	facts["needs_confirm"] = needConfirm
	if !in.Apply {
		msg := fmt.Sprintf("Dry run: would %s %s (risk %s). Nothing was changed; run again with apply=true", map[bool]string{true: "clear the override of", false: "set"}[in.Clear], strings.ToUpper(name), risk)
		if needConfirm {
			msg += fmt.Sprintf(" and confirm=%q (a %s property needs it)", strings.ToUpper(name), risk)
		}
		return result.Build(editOrgPropertyName, result.OK, msg+". Undo: "+undo, result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(facts), NextActions: []string{"inspect-environment", "inspect-snapshots"}})
	}
	if needConfirm && !confirmed {
		return result.Build(editOrgPropertyName, result.Failed, fmt.Sprintf("Refused, nothing was changed: %s is classified %s (%s); apply needs confirm=%q", strings.ToUpper(name), risk, reason, strings.ToUpper(name)),
			result.Deterministic, cx, result.Options{Mode: mode, Limits: limits, Evidence: evd(facts)})
	}
	var err error
	if in.Clear {
		err = s.ClearOrgProperty(ctx, name)
	} else {
		err = s.SetOrgProperty(ctx, name, in.Value)
	}
	if err != nil {
		return result.Result{}, fmt.Errorf("the change failed, nothing is known to have changed (a 403 means this login lacks the organization-settings permission, or the property is not changeable by an organization administrator here): %w", err)
	}
	ch.Applied = true
	now, rerr := s.EffectiveOrgConfig(ctx)
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the change was sent but reading the configuration back failed, so it is not proven: %w", rerr)
	}
	want := in.Value
	if in.Clear {
		want = def
	}
	if got := now[name]; !sameOrgValue(got, want) {
		return result.Build(editOrgPropertyName, result.Failed, fmt.Sprintf("Forward accepted the change but %s reads %q, not %q. Undo: %s", strings.ToUpper(name), got, want, undo),
			result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(facts)})
	}
	return result.Build(editOrgPropertyName, result.OK, fmt.Sprintf("Applied: %s is now %q (organization-wide). Undo: %s", strings.ToUpper(name), now[name], undo), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: evd(facts), NextActions: []string{"inspect-environment", "inspect-snapshots"}})
}

func hasKnownUse(uses []string, u string) bool {
	for _, x := range uses {
		if x == u {
			return true
		}
	}
	return false
}

func whoCanChange(admin string) string {
	switch admin {
	case "ANYWHERE":
		return "an organization administrator (SaaS or on-premises) or Forward support"
	case "ON_PREMISES":
		return "an organization administrator of an on-premises Forward, or Forward support (not on SaaS)"
	case "NONE":
		return "Forward support only"
	}
	return "unknown (this build holds no definition of the property)"
}

func sameOrgValue(a, b string) bool {
	return strings.EqualFold(strings.Trim(a, `"`), strings.Trim(b, `"`))
}

// validOrgValue checks a value against what Forward's source says the property accepts; it returns why it refuses, or "". Without a definition the value's shape must at least match
// the current value's, and the caller must confirm (the property is unclassified).
func validOrgValue(info *knowledge.OrgProp, known bool, current, value string) string {
	v := strings.TrimSpace(value)
	kind := ""
	if known {
		kind = info.Kind
	} else {
		switch {
		case current == "true" || current == "false":
			kind = "boolean"
		case isInt(current):
			kind = "integer"
		}
	}
	switch kind {
	case "boolean":
		if v != "true" && v != "false" {
			return fmt.Sprintf("%q is not a boolean; give true or false", v)
		}
	case "integer":
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Sprintf("%q is not an integer", v)
		}
		if known && len(info.Range) == 2 {
			lo, _ := strconv.ParseInt(info.Range[0], 10, 64)
			hi, _ := strconv.ParseInt(info.Range[1], 10, 64)
			if n < lo || n > hi {
				return fmt.Sprintf("%d is outside the range %d to %d Forward accepts", n, lo, hi)
			}
		}
	case "number":
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return fmt.Sprintf("%q is not a number", v)
		}
	case "enum":
		if len(info.Values) == 0 {
			return "Forward's source lists no values for this enumeration, so a value cannot be checked"
		}
		for _, x := range info.Values {
			if strings.EqualFold(x, v) {
				return ""
			}
		}
		return fmt.Sprintf("%q is not one of %s", v, strings.Join(info.Values, ", "))
	case "other":
		return "this property's value has a format this skill does not model; it is not set here"
	}
	return ""
}

func isInt(s string) bool {
	_, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return err == nil
}

// listOrgProperties is the read mode: every property Forward reports, with its value, whether the org overrides it, the default, who may change it and how risky it is.
func listOrgProperties(cfg fwd.OrgConfig, in editOrgPropertyInput, cx result.Context) (result.Result, error) {
	names := make([]string, 0, len(cfg.Effective))
	for n := range cfg.Effective {
		names = append(names, n)
	}
	sort.Strings(names)
	counts := map[string]int{}
	var rows []map[string]any
	m := strings.ToLower(strings.TrimSpace(in.Match))
	for _, n := range names {
		info, known := knowledge.OrgPropertyInfo(n)
		risk, reason, admin, doc := "unclassified", "no reviewed classification: treat as caution", "unknown", ""
		if known {
			risk, reason, admin, doc = info.Risk, info.Reason, info.Admin, info.Doc
		}
		if m != "" && !strings.Contains(n, m) && !strings.Contains(strings.ToLower(doc), m) {
			continue
		}
		_, overridden := cfg.Configured[n]
		counts[risk]++
		row := map[string]any{"property": strings.ToUpper(n), "value": clipValue(cfg.Effective[n]), "overridden": overridden, "default": clipValue(cfg.Global[n]), "risk": risk, "reason": reason, "who_can_change": whoCanChange(admin)}
		if doc != "" {
			row["description"] = doc
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return result.NewUnknown(editOrgPropertyName, "No organization property matches", cx, []string{"the list is Forward's own; match is a substring of the name or its description"}, result.Options{})
	}
	win, limits, ok := window(rows, in.Limit, in.Offset, 50, 200, "properties")
	if !ok {
		return result.NewUnknown(editOrgPropertyName, fmt.Sprintf("Offset %d is beyond the %d properties", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	limits = append(limits, "risk is a reviewed classification of what changing a property can break (dangerous, caution, safe) or unclassified (treat as caution): dangerous and unclassified properties need confirm to apply; who_can_change comes from Forward's source and an on-premises-only property is refused on SaaS")
	finding := fmt.Sprintf("%d organization properties (%d dangerous, %d caution, %d safe, %d unclassified)", len(rows), counts["dangerous"], counts["caution"], counts["safe"], counts["unclassified"])
	d := map[string]any{"total": len(rows), "by_risk": counts, "offset": in.Offset, "properties": win}
	return result.Build(editOrgPropertyName, result.OK, finding, result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "orgConfig", nil, d, finding)}, NextActions: []string{"inspect-environment"}})
}

// clipValue keeps a long value (a list of addresses) from filling a listing; a run with the property in it shows the whole value.
func clipValue(v string) string {
	if len(v) <= 120 {
		return v
	}
	return v[:120] + fmt.Sprintf("... (%d characters; run the property to see all of it)", len(v))
}
