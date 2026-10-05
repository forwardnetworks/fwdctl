package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const environmentName = "inspect-environment"

func init() { Register(environmentName, inspectEnvironment) }

type environmentInput struct {
	Properties bool `json:"include_properties"`
	// NoFeatures skips the feature table (include_features false). It is on by default: it is five reads of the organization configuration.
	Features *bool `json:"include_features"`
}

// inspectEnvironment states what the session is talking to: the Forward build, the organization and login, the capabilities the
// SDK has seen, the vulnerability index age and (on request) the organization properties that differ from the defaults. It is
// account-level, and it lists no users, tokens or roles.
func inspectEnvironment(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in environmentInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
	}
	cx := result.Context{Scope: "account", State: "current"}
	detail := map[string]any{}
	var limits []string
	var omitted []result.Omission
	read := 0

	if v, err := s.Version(ctx); err != nil {
		limits = append(limits, "the Forward version could not be read: "+err.Error())
	} else {
		read++
		detail["version"] = map[string]any{"version": v.Version, "release": v.Release, "build": v.Build}
	}
	if o, err := s.Organization(ctx); err != nil {
		limits = append(limits, "the organization could not be read: "+err.Error())
	} else {
		read++
		detail["organization"] = map[string]any{"id": string(o.ID), "name": o.Name, "disabled": o.Disabled}
	}
	if u, err := s.CurrentUser(ctx); err != nil {
		limits = append(limits, "the login could not be read: "+err.Error())
	} else {
		read++
		detail["login"] = map[string]any{"username": u.Username}
	}
	if m, err := s.CVEIndex(ctx); err != nil {
		limits = append(limits, "the vulnerability index metadata could not be read: "+err.Error())
	} else {
		read++
		updated := m.IndexUploadedAt != ""
		detail["cve_index"] = map[string]any{"built_at": m.IndexCreatedAt, "updated_since_install": updated, "uploaded_at": m.IndexUploadedAt}
		if !updated {
			limits = append(limits, "the vulnerability index is the one bundled with this build and was never updated, so newer CVEs are not in it")
		}
	}
	if nets, err := s.Networks(ctx); err != nil {
		limits = append(limits, "the networks could not be listed: "+err.Error())
	} else {
		read++
		detail["network_count"] = len(nets)
	}
	caps := map[string]string{}
	for _, c := range s.CapabilityStatuses() {
		caps[string(c.Capability)] = string(c.Support)
	}
	detail["capabilities"] = caps
	limits = append(limits, "capabilities are what the SDK has learned so far: unknown means not yet seen, not unsupported")

	if in.Properties {
		if p, names, err := s.NonDefaultProperties(ctx); err != nil {
			limits = append(limits, "the organization properties could not be read: "+err.Error())
		} else {
			read++
			shown, capped := result.Cap(names, 50, "non-default properties", "the first 50 by name are shown")
			omitted = append(omitted, capped...)
			vals := map[string]string{}
			for _, n := range shown {
				vals[n] = p[n]
			}
			detail["non_default_properties"] = vals
			expl, missing := explainProperties(shown, vals)
			detail["explanations"] = expl
			if len(missing) > 0 {
				limits = append(limits, "no explanation is held for these properties (read Forward's documentation): "+strings.Join(missing, ", "))
			}
			limits = append(limits, "explanations are Forward's own property documentation (or, where it is empty, what its source does) with the skill behaviour each one changes; see reference/properties.md")
			detail["non_default_property_count"] = len(names)
		}
	}
	if in.Features == nil || *in.Features {
		block, flimits := featuresBlock(s.OrgConfig(ctx))
		detail["features"] = block
		limits = append(limits, flimits...)
	}
	if read == 0 {
		return result.NewUnknown(environmentName, "Nothing about the Forward environment could be read", cx, limits, result.Options{})
	}
	finding := "Forward environment read"
	if v, ok := detail["version"].(map[string]any); ok {
		finding = fmt.Sprintf("Forward %v (%v)", v["version"], v["release"])
	}
	return result.Build(environmentName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: []string{"inspect-networks"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvState, "inspectEnvironment", nil, detail, finding)}})
}
