package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/knowledge"
)

// orgPropertyRows is the org_properties area of inspect-platform: every property the appserver defines for one organization, with its effective value and where that value comes
// from. Forward exposes no route for the compiled default, so source "compiled_default" means "not set on the org and not a global override", and the value itself is the default.
func orgPropertyRows(ctx context.Context, s *fwd.Session, in inspectPlatformInput) ([]any, []string, error) {
	orgID, lim, err := resolveOrgID(ctx, s, in)
	if err != nil {
		return nil, lim, err
	}
	cat, _, err := s.Client.Properties.DescribeOrganization(ctx, orgID)
	if err != nil {
		return nil, lim, err
	}
	var rows []any
	if v, _, verr := s.Client.Version.Get(ctx); verr == nil && v != nil {
		rows = append(rows, map[string]any{"row": "build", "name": "appserver_build", "build": v.Build, "release": v.Release, "version": v.Version, "org_id": orgID})
	} else {
		lim = append(lim, fmt.Sprintf("the appserver build was not read (GET /api/version: %v)", verr))
	}
	global := map[string]bool{}
	globalKnown := false
	if g, _, gerr := s.Client.Properties.Global(ctx, forward.PropertyFilterConfigured); gerr == nil {
		globalKnown = true
		for k := range g {
			global[strings.ToUpper(strings.TrimSpace(string(k)))] = true
		}
	} else {
		lim = append(lim, fmt.Sprintf("global overrides were not read (%v), so a property not set on the organization is reported as global_or_compiled_default", gerr))
	}
	release := ""
	if v, _, verr := s.Client.Version.Get(ctx); verr == nil && v != nil {
		release = releaseOf(firstNonEmpty(v.Release, v.Version))
	}
	for _, p := range cat.Properties {
		name := string(p.Name)
		m := map[string]any{"row": "property", "name": name, "effective": p.Value, "kind": string(p.Kind)}
		if info, ok := knowledge.OrgPropertyInfo(name); ok {
			m["who_can_change"] = whoCanChange(info.Admin)
			if hasKnownUse(info.Uses, "COMPUTATION") {
				m["applies_to_snapshots_processed_after_change"] = true // a snapshot already processed shows the old value until it is reprocessed
			}
			if dv, defined, known := knowledge.OrgPropertyDefault(name, release); known && defined {
				m["release_default"], m["default_release"] = dv, release
			}
		}
		switch {
		case p.Configured:
			m["source"], m["org_value"] = "org", p.ConfiguredValue
		case cat.LayersErr == nil && p.Layer == forward.PropertyLayerGlobal:
			m["source"] = "global_override"
		case cat.LayersErr == nil && p.Layer == forward.PropertyLayerCompiled:
			m["source"] = "compiled_default"
		case !globalKnown:
			m["source"] = "global_or_compiled_default"
		case global[name]:
			m["source"] = "global_override"
		default:
			m["source"] = "compiled_default"
		}
		if p.HasDefault {
			m["deployment_default"] = p.Default // a global override if one is set, else the compiled default
		}
		if cat.LayersErr == nil {
			m["non_default"] = p.NonDefault // differs from Forward's built-in default
			if p.HasCompiledDefault {
				m["built_in_default"] = p.CompiledDefault
			}
		}
		rows = append(rows, m)
		switch name {
		case "PREDICT_MODEL_EXT_ADV":
			if p.Value == "true" && m["source"] != "org" {
				lim = append(lim, "PREDICT_MODEL_EXT_ADV is true without an organization setting: it is the "+fmt.Sprint(m["source"])+" value, and some builds compile it as true, so an absent org row does not mean false")
			}
		case "PREDICT_MODEL_CONFIG":
			lim = append(lim, "PREDICT_MODEL_CONFIG: a change to it takes effect on a snapshot only after the snapshot is reprocessed")
		}
	}
	if cat.LayersErr != nil {
		lim = append(lim, fmt.Sprintf("the reads that tell a global override from the built-in default failed (%v), so non_default and built_in_default are absent and source falls back to the global override list", cat.LayersErr))
	} else {
		lim = append(lim, "source and non_default come from Forward's layers: org (this organization's own row), global_override (a deployment-wide default), compiled_default (Forward's built-in default); the values come from five reads in turn, not one snapshot")
	}
	if cat.DefaultsErr != nil {
		lim = append(lim, fmt.Sprintf("deployment defaults were not read (%v), so deployment_default is absent", cat.DefaultsErr))
	}
	lim = append(lim, "release_default and who_can_change come from Forward's OrgProperty.java on the release branches (read from source, not run on a live Forward); a property whose who_can_change is Forward support, or on-premises only on a SaaS tenant, is set by Forward support (PUT /api/orgs/{orgId}/config/{property}), not by an organization administrator")
	lim = append(lim, "organization "+orgID+": effective and org-set values were read from Forward; the compiled default is not exposed by any route, so for a property with source compiled_default the effective value is that default",
		"to compare two organizations or clusters, run this once on each and compare the rows by name (and the appserver_build row); this call reads one organization of one Forward")
	return rows, lim, nil
}

// resolveOrgID takes org_id, else the organization of network_id, else the one organization this login can see.
func resolveOrgID(ctx context.Context, s *fwd.Session, in inspectPlatformInput) (string, []string, error) {
	switch {
	case in.OrgID != "":
		return in.OrgID, nil, nil
	case in.NetworkID != "":
		nets, err := s.Networks(ctx)
		if err != nil {
			return "", nil, err
		}
		for i := range nets {
			if idOf(&nets[i]) == in.NetworkID {
				if nets[i].OrgID == "" {
					return "", nil, fmt.Errorf("%w: Forward gave no organization for network %s: give org_id", ErrInvalidInput, in.NetworkID)
				}
				return string(nets[i].OrgID), nil, nil
			}
		}
		return "", nil, fmt.Errorf("%w: network %s is not visible to this login: give org_id", ErrInvalidInput, in.NetworkID)
	}
	orgs, _, err := s.Client.Organizations.List(ctx)
	if err != nil {
		return "", nil, err
	}
	if len(orgs) != 1 {
		return "", nil, fmt.Errorf("%w: this login sees %d organizations: give org_id or network_id", ErrInvalidInput, len(orgs))
	}
	return string(orgs[0].ID), nil, nil
}

// orgPropertyOverrides is area org_properties with view overrides: ONE property (name) and the value every organization this login can see
// overrides it to (Forward support: a support login sees the organizations it supports, a Forward administrator all). An organization not
// listed has no override and takes the default.
func orgPropertyOverrides(ctx context.Context, s *fwd.Session, in inspectPlatformInput) ([]any, []string, error) {
	prop := strings.ToUpper(strings.TrimSpace(in.Name))
	if prop == "" {
		return nil, nil, fmt.Errorf("%w: view overrides reads one property across organizations: give name (the property, such as COPY_CREDENTIALS)", ErrInvalidInput)
	}
	if in.OrgID != "" || in.NetworkID != "" {
		return nil, nil, fmt.Errorf("%w: view overrides reads every organization this login can see; it takes no org_id or network_id", ErrInvalidInput)
	}
	over, _, err := s.Client.Properties.OrganizationOverrides(ctx, forward.OrgProperty(prop))
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(over))
	for id := range over {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := make([]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]any{"row": "override", "name": prop, "org_id": id, "org_value": strings.Trim(string(over[id]), `"`)})
	}
	return rows, []string{"an organization that is not listed has no override of " + prop + " and takes the default (inspect-platform area org_properties with org_id shows which default)",
		"a Forward support login sees only the organizations it supports; a Forward administrator sees all"}, nil
}
