package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
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
	for _, p := range cat.Properties {
		name := string(p.Name)
		m := map[string]any{"row": "property", "name": name, "effective": p.Value, "kind": string(p.Kind)}
		switch {
		case p.Configured:
			m["source"], m["org_value"] = "org", p.ConfiguredValue
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
	if cat.DefaultsErr != nil {
		lim = append(lim, fmt.Sprintf("deployment defaults were not read (%v), so deployment_default is absent", cat.DefaultsErr))
	}
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
