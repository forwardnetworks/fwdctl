package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// featureDef is one organization property the skills depend on, or that gates preview behaviour they touch. Level and Admin are Forward's own
// declarations (its property catalogue: level ORG = an override per organization only, BOTH = a per-organization override and an overridable deployment default;
// OrgAdminConfigurability NONE = only Forward support or admins can change it, ON_PREMISES = an organization administrator of an on-premises Forward, ANYWHERE = an
// organization administrator on any deployment). Reprocess is Forward's use COMPUTATION: changing the value reaches existing snapshots only after they are reprocessed.
//
// How the effective value reads as a state: "flag" true = on; "inverted" true = the feature is switched OFF (disable_*); "mode" the value itself; "value" a number or
// enum with no on or off.
type featureDef struct {
	Property, Feature, Skills, Kind string
	Level, Admin                    string
	Reprocess                       bool
	Symptom                         string
}

var featureDefs = []featureDef{
	{"advanced_reachability_analysis", "advanced reachability (the flow DAG): when it is computed", "inspect-snapshots, inspect-vulnerabilities (internet_addressable), edit-advanced-reachability", "mode", "BOTH", "ON_PREMISES", true,
		"ON_DEMAND (the default): nothing computes it automatically; a snapshot reads advanced_reachability UNPROCESSED and internet_addressable is unavailable (PENDING_ADVANCED_REACHABILITY) until edit-advanced-reachability runs for it. ASYNC starts it after a snapshot is processed, except predicted snapshots and a network with flow computation disabled"},
	{"disable_flow_computation", "flow (reachability) computation", "investigate-reachability, verify-change, inspect-vulnerabilities (internet_addressable), advanced reachability", "inverted", "BOTH", "ON_PREMISES", true,
		"flow computation is switched off: no path search or flow analysis, and internet exposure is REACHABILITY_COMPUTATION_DISABLED once the DAG stage finished (before that it reads PENDING_ADVANCED_REACHABILITY, which looks like never triggered). A license without path analysis has the same effect and is not readable here"},
	{"reachability_timeout_minutes", "time allowed to the reachability and advanced reachability computation", "inspect-snapshots, edit-advanced-reachability", "value", "BOTH", "ON_PREMISES", true,
		"a computation longer than this ends TIMED_OUT, a final state: edit-advanced-reachability refuses it until the snapshot is reprocessed"},
	{"reachability_max_concurrent_devices_per_worker", "devices one worker computes flowlets for at once", "edit-advanced-reachability (its cost), inspect-snapshots", "value", "ORG", "ON_PREMISES", true,
		"a low value makes processing and advanced reachability slow; a high one risks the worker running out of memory"},
	{"proactive_internet_connection_suggestions_computation", "internet connection suggestions after processing", "inspect-topology kind external, plan-synthetic-device", "flag", "ORG", "ON_PREMISES", false,
		"Forward does not compute suggestions after processing, so an empty suggestion list means none were computed, not none are needed"},
	{"predict_modeling", "Predict: change sets, predicted snapshots and their checks", "edit-change-set, verify-change", "flag", "BOTH", "ON_PREMISES", true,
		"change-set edits, checks and predicted snapshots are not offered, so a change set cannot be predicted or reviewed"},
	{"predict_model_bgp", "Predict models BGP", "edit-change-set (BGP advertisements), verify-change", "flag", "BOTH", "ON_PREMISES", true,
		"a prediction does not carry the effect of a BGP change"},
	{"predict_model_ospf", "Predict models OSPF", "edit-change-set, verify-change", "flag", "BOTH", "ON_PREMISES", true,
		"a prediction does not carry the effect of an OSPF change"},
	{"predict_model_ext_adv", "Predict seeds routes from the IBGP and EBGP route tables", "edit-change-set (BGP advertisements), verify-change", "flag", "BOTH", "ON_PREMISES", true,
		"externally advertised routes are not part of a prediction"},
	{"link_override_staging", "link-override edits staged without invalidating snapshots (network level)", "edit-link-overrides, plan-link-overrides", "flag", "ORG", "ON_PREMISES", false,
		"Forward documents it as UI-gated: when off, editing a snapshot's overrides invalidates it, and staging at network level is not offered"},
	{"copy_credentials", "credentials copied into a new workspace network", "edit-workspace", "value", "ORG", "ANYWHERE", false,
		"DISABLED (the default): a new workspace gets no credential, cloud account or vCenter, so a person must create credentials inside it; ENABLED copies ALL of the parent's secrets; ENABLED_FOR_ADMINS does so only when the creator is an org admin or an admin of the parent"},
	{"performance_data", "performance data from SNMP collection", "inspect-performance", "flag", "ORG", "ANYWHERE", false,
		"performance data is not shown, so inspect-performance has nothing to read"},
	{"enable_cve_config_analysis", "configuration analysis that filters device vulnerability findings", "inspect-vulnerabilities", "flag", "BOTH", "ON_PREMISES", false,
		"findings are not narrowed by device configuration, so more CVEs are listed than apply"},
	{"auto_update_cve_index", "automatic updates of the vulnerability index", "inspect-vulnerabilities, inspect-environment (cve_index)", "flag", "BOTH", "ON_PREMISES", false,
		"the index stays the one bundled with the build unless someone uploads one, so newer CVEs are missing"},
	{"nqe_security_rules_panos", "NQE exposes PAN-OS security rules", "author-nqe-query, validate-nqe-query", "flag", "BOTH", "ON_PREMISES", false,
		"a query over PAN-OS security rules compiles but returns no rows, which is not the same as no rules"},
	{"nqe_security_rules_fortios", "NQE exposes FortiOS security rules", "author-nqe-query, validate-nqe-query", "flag", "BOTH", "ON_PREMISES", false,
		"a query over FortiOS security rules compiles but returns no rows, which is not the same as no rules"},
	{"nqe_lldp_chassis_id", "NQE exposes the LLDP chassis ID of a peer", "author-nqe-query, validate-nqe-query", "flag", "ORG", "ON_PREMISES", false,
		"the field is not populated in NQE results"},
	{"peer_lldp_cdp_capability", "NQE exposes the capabilities a link peer advertises over LLDP or CDP", "author-nqe-query, validate-nqe-query", "flag", "BOTH", "ON_PREMISES", false,
		"the field is not populated in NQE results"},
	{"snapshot_reprocess_minimum_role", "the least role that may reprocess a snapshot or start advanced reachability", "edit-snapshot, edit-advanced-reachability", "value", "ORG", "ANYWHERE", false,
		"a login below this role is refused (403); organization administrators are not subject to it"},
	{"background_snapshot_reprocess", "priority of background reprocessing", "edit-snapshot, inspect-snapshots", "value", "BOTH", "ON_PREMISES", false,
		"LOW_PRIORITY lets a reprocessed or invalidated snapshot wait behind other work, so it stays PROCESSING longer"},
	{"allowed_onprem_collectors", "which kinds of on-premises collector may be used", "inspect-collection, edit-collection", "value", "BOTH", "ON_PREMISES", false,
		"a collection cannot start until an allowed kind of collector is connected"},
}

// featureState says how a property's effective value reads for its feature.
func featureState(d featureDef, v string) string {
	b := strings.EqualFold(strings.TrimSpace(v), "true")
	switch d.Kind {
	case "flag":
		if b {
			return "on"
		}
		return "off"
	case "inverted":
		if b {
			return "off"
		}
		return "on"
	case "mode":
		if strings.EqualFold(v, "ASYNC") {
			return "automatic"
		}
		return "on request only"
	}
	return "value"
}

func adminWho(admin string) string {
	switch admin {
	case "ANYWHERE":
		return "an organization administrator (any deployment) or Forward support"
	case "ON_PREMISES":
		return "an organization administrator of an on-premises Forward, or Forward support; not on SaaS"
	}
	return "Forward support or a Forward admin only (not an organization administrator)"
}

// featureSource says at which level the effective value comes from, as far as the API tells: an organization override, a deployment-level change of the
// default (the deployment default differs from the value compiled into the build, so every organization without an override gets it), or the built-in default.
// It is empty when the views needed were not read. It never says who set it or when: the API does not.
func featureSource(c fwd.OrgConfig, prop string) string {
	if c.Overridden == nil || c.NonDefault == nil || c.Configured == nil {
		return ""
	}
	if _, ok := c.Overridden[prop]; ok {
		return "organization override"
	}
	if _, ok := c.Configured[prop]; ok {
		return "organization row equal to the deployment default"
	}
	if _, ok := c.NonDefault[prop]; ok {
		return "deployment default (differs from the built-in default)"
	}
	return "built-in default"
}

// featuresBlock renders the curated feature table from what the configuration API returned. Nothing here is guessed: a property the API does not return is
// said to be not returned, and a source that could not be read is named.
func featuresBlock(c fwd.OrgConfig) (map[string]any, []string) {
	var limits []string
	sources := map[string]string{}
	for _, name := range []string{"effective", "configured", "overridden", "nondefault", "global"} {
		if why, bad := c.Errs[name]; bad {
			sources[name] = "not read: " + why
		} else {
			sources[name] = "read"
		}
	}
	block := map[string]any{"sources": sources}
	if c.Effective == nil {
		limits = append(limits, "the effective organization properties could not be read ("+c.Errs["effective"]+"), so the state of every feature is unknown")
		block["rows"] = []any{}
		return block, limits
	}
	var rows []map[string]any
	var missing []string
	for _, d := range featureDefs {
		v, ok := c.Effective[d.Property]
		if !ok {
			missing = append(missing, d.Property)
			continue
		}
		state := featureState(d, v)
		row := map[string]any{"property": d.Property, "feature": d.Feature, "value": v, "state": state, "level": d.Level, "org_admin_configurability": d.Admin}
		if c.Global != nil {
			row["default"] = c.Global[d.Property]
		}
		if c.Overridden != nil {
			_, o := c.Overridden[d.Property]
			row["overridden_at_org"] = o
		}
		if d.Reprocess {
			row["reprocess_to_apply"] = true
		}
		if src := featureSource(c, d.Property); src != "" {
			row["set_at"] = src
		}
		switch {
		case state == "value":
			// a number or an enum has no off: say what it does only when someone changed it from the built-in default
			if src, _ := row["set_at"].(string); src != "" && src != "built-in default" {
				row["effect_of_a_change"] = d.Symptom
				row["who_can_change_it"] = adminWho(d.Admin)
			}
		case state != "on":
			row["symptom_when_off"] = d.Symptom
			row["who_can_change_it"] = adminWho(d.Admin)
		}
		rows = append(rows, row)
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	block["rows"] = out
	if len(missing) > 0 {
		sort.Strings(missing)
		block["not_defined_by_this_build"] = missing
		limits = append(limits, "these properties are not defined by this Forward build (it does not return them), so the feature they gate is absent or always on: "+strings.Join(missing, ", "))
	}
	for _, name := range []string{"global", "overridden", "configured", "nondefault"} {
		if why, bad := c.Errs[name]; bad {
			limits = append(limits, "the "+name+" view of the organization properties could not be read ("+why+"): the fields built from it are left out, not guessed")
		}
	}
	limits = append(limits,
		"features are read from Forward's organization configuration API: it says what is set now for this organization and the deployment default, not who set a value, when, or why",
		"not returned by any API: network-level overrides of disable_flow_computation and advanced_reachability_analysis, and per-user toggles; a feature that reads on here can still be off for one network or unlicensed (see license)",
		"a property marked reprocess_to_apply changes existing snapshots only after they are reprocessed; a changed value does not say which snapshots were computed under the old one")
	return block, limits
}

// licenseFacets reads a license tier's letters as its facets: N network, S security, P predict (a peer session read Forward's LicenseTier: NS has no Predict, only NP and NSP do). A tier whose letters are not these is reported as is, with no facet claimed.
func licenseFacets(tier string) (map[string]bool, bool) {
	t := strings.ToUpper(strings.TrimSpace(tier))
	if t == "" || strings.Trim(t, "NSP") != "" {
		return nil, false
	}
	return map[string]bool{"network": strings.Contains(t, "N"), "security": strings.Contains(t, "S"), "predict": strings.Contains(t, "P")}, true
}

// applyLicense adds the license (tier, status, facets, whether it can write) to the environment and marks the Predict features unlicensed when the tier lacks the facet, even though their organization property reads on. It
// returns the limits.
func applyLicense(ctx context.Context, s *fwd.Session, detail map[string]any, block map[string]any) []string {
	ts, _, err := s.Client.Licensing.TierAndStatus(ctx)
	if err != nil || ts == nil {
		reason := "unreadable"
		if err != nil {
			reason = err.Error()
		}
		return []string{"the license tier and status could not be read (" + reason + "), so which features are licensed is not known"}
	}
	lic := map[string]any{"tier": ts.LicenseTier, "status": ts.LicenseStatus}
	var limits []string
	switch ts.LicenseStatus {
	case "ACTIVE", "EXEMPT", "SOFTWARE_CENTRAL":
		lic["can_write"] = true
	default:
		lic["can_write"] = false
		lic["note"] = "writes need a license status of ACTIVE, EXEMPT or SOFTWARE_CENTRAL; this one is " + ts.LicenseStatus
	}
	facets, known := licenseFacets(ts.LicenseTier)
	if known {
		lic["facets"] = facets
		if rows, ok := block["rows"].([]any); ok {
			for _, r := range rows {
				row, _ := r.(map[string]any)
				if row == nil || !strings.HasPrefix(fmt.Sprint(row["property"]), "predict_") {
					continue
				}
				row["licensed"] = facets["predict"]
				if !facets["predict"] {
					if row["state"] == "on" || row["state"] == "automatic" {
						row["licensed_note"] = "the organization property reads on, but license tier " + ts.LicenseTier + " has no Predict facet: change-set calls answer 403 \"Unlicensed operation\" (a license limit, not a role)"
					} else {
						row["licensed_note"] = "off, and also unlicensed: license tier " + ts.LicenseTier + " has no Predict facet, so turning the property on would not make change-set calls work (they answer 403 \"Unlicensed operation\", a license limit, not a role)"
					}
				}
			}
		}
	} else if ts.LicenseTier != "" {
		limits = append(limits, "license tier "+ts.LicenseTier+" is not one whose letters (N network, S security, P predict) are known here, so no facet is claimed")
	} else {
		limits = append(limits, "the license tier is empty: the organization is unlicensed, and a licensed operation will be refused")
	}
	limits = append(limits, "the license tier and status are Forward's (GET /api/licenses?view=status); reading the tier's letters as facets (N network, S security, P predict) is from a peer session's reading of Forward's source, not confirmed on a live Forward")
	detail["license"] = lic
	return limits
}
