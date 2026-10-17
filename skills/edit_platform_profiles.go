package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// planEndpointProfile creates, updates and deletes endpoint profiles: CLI (the commands Forward runs on the endpoints that use one), SNMP (detector OID and patterns, name detector, custom OIDs) and HTTP
// (GET paths, Basic auth or none). A profile is organization-wide: every network sees it. edit-endpoint-profile copies an SNMP profile and assigns endpoints in one step; assigning endpoints to a profile
// is edit-source object endpoint.
func planEndpointProfile(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	profiles, err := s.EndpointProfiles(ctx)
	if err != nil {
		return nil, err
	}
	find := func(key string) *forward.EndpointProfile {
		for i := range profiles {
			if string(profiles[i].ID) == key || strings.EqualFold(profiles[i].Name, key) {
				return &profiles[i]
			}
		}
		return nil
	}
	if in.Action == "create" {
		def, err := withProfileName(in.Definition, in.Name)
		if err != nil {
			return nil, err
		}
		in.Definition = def
	}
	if plan, handled, err := planRichEndpointProfile(ctx, s, in, profiles, find); handled {
		return plan, err
	}
	switch in.Action {
	case "create":
		var def struct {
			Name           *string  `json:"name"`
			Type           string   `json:"type"`
			CommandSets    []string `json:"command_sets"`
			CustomCommands []string `json:"custom_commands"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, type (CLI, the only type created here), command_sets (UNIX), custom_commands"); err != nil {
			return nil, err
		}
		if def.Type != "" && !strings.EqualFold(def.Type, "CLI") {
			return nil, fmt.Errorf("%w: type is CLI, SNMP or HTTP", ErrInvalidInput)
		}
		if def.Name == nil || strings.TrimSpace(*def.Name) == "" || len(def.CommandSets)+len(def.CustomCommands) == 0 {
			return nil, fmt.Errorf("%w: create needs a name (top-level name, or definition.name) and command_sets or custom_commands", ErrInvalidInput)
		}
		sets := make([]string, 0, len(def.CommandSets))
		for _, c := range def.CommandSets {
			u := strings.ToUpper(strings.TrimSpace(c))
			if u != "UNIX" {
				return nil, fmt.Errorf("%w: command set %q is not one of UNIX (the only predefined set: ifconfig, netstat -ln, hostname, uname -a)", ErrInvalidInput, c)
			}
			sets = append(sets, u)
		}
		name := strings.TrimSpace(*def.Name)
		if find(name) != nil {
			return nil, fmt.Errorf("%w: an endpoint profile named %q already exists (names are unique ignoring case)", ErrInvalidInput, name)
		}
		cmds := append([]string{}, def.CustomCommands...)
		req := forward.EndpointProfileRequest{Name: name, Type: "CLI", CommandSets: sets, CustomCommands: cmds}
		limits := []string{"a profile is ORGANIZATION-wide: every network sees it; only the endpoints assigned to it (edit-source object endpoint) use it",
			"a profile is collected only if all of its commands are in the organization's approved CLI command list, which is replaced only by a Forward-signed file"}
		if len(cmds) > 0 {
			if a, _, aerr := s.Client.Endpoints.AssessCLICommands(ctx, cmds); aerr != nil {
				limits = append(limits, "whether the custom commands are approved could not be read: "+aerr.Error())
			} else if len(a.Unapproved) > 0 {
				limits = append(limits, fmt.Sprintf("not approved by the organization, so endpoints with this profile will not be collected: %s", strings.Join(a.Unapproved, "; ")))
			}
		}
		var id string
		return &networkPlan{target: "create CLI endpoint profile " + name, action: "create_endpoint_profile", after: req, reversible: true,
			undo: "delete the profile (action delete, name its id, confirm its id) after reassigning its endpoints", limits: limits,
			do: func(ctx context.Context) error {
				p, _, err := s.Client.Endpoints.CreateProfile(ctx, req)
				if p != nil {
					id = string(p.ID)
				}
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := s.EndpointProfiles(ctx)
				for _, p := range now {
					if strings.EqualFold(p.Name, name) {
						return true, map[string]any{"id": p.ID, "name": p.Name, "type": p.Type, "command_sets": p.CommandSets}, err
					}
				}
				_ = id
				return false, len(now), err
			}}, nil
	case "update":
		p := find(in.Name)
		if p == nil {
			return nil, fmt.Errorf("%w: no endpoint profile %q (inspect-platform area endpoint_profiles lists them)", ErrInvalidInput, in.Name)
		}
		if p.Type != "CLI" {
			return nil, fmt.Errorf("%w: %s is a %s profile; give a CLI, SNMP or HTTP profile", ErrInvalidInput, p.ID, p.Type)
		}
		var def struct {
			CommandSets    []string `json:"command_sets"`
			CustomCommands []string `json:"custom_commands"`
		}
		if err := decodeDefinition(in.Definition, &def, "command_sets (UNIX), custom_commands: each replaces the profile's current list"); err != nil {
			return nil, err
		}
		if def.CommandSets == nil && def.CustomCommands == nil {
			return nil, fmt.Errorf("%w: update needs command_sets or custom_commands", ErrInvalidInput)
		}
		patch := forward.EndpointProfilePatch{}
		if def.CommandSets != nil {
			sets := make([]string, 0, len(def.CommandSets))
			for _, c := range def.CommandSets {
				u := strings.ToUpper(strings.TrimSpace(c))
				if u != "UNIX" {
					return nil, fmt.Errorf("%w: command set %q is not one of UNIX", ErrInvalidInput, c)
				}
				sets = append(sets, u)
			}
			patch.CommandSets = sets
		}
		if def.CustomCommands != nil {
			patch.CustomCommands = append([]string{}, def.CustomCommands...)
		}
		limits := []string{"the change applies to every endpoint that uses this profile, in every network of the organization, from its next collection",
			"a profile is collected only if all of its commands are in the organization's approved CLI command list"}
		if len(patch.CustomCommands) > 0 {
			if a, _, aerr := s.Client.Endpoints.AssessCLICommands(ctx, patch.CustomCommands); aerr != nil {
				limits = append(limits, "whether the custom commands are approved could not be read: "+aerr.Error())
			} else if len(a.Unapproved) > 0 {
				limits = append(limits, fmt.Sprintf("not approved by the organization, so endpoints with this profile will not be collected: %s", strings.Join(a.Unapproved, "; ")))
			}
		}
		before, _ := fwd.Generic(*p)
		id := string(p.ID)
		return &networkPlan{target: "update CLI endpoint profile " + id, action: "update_endpoint_profile", before: before, after: map[string]any{"command_sets": patch.CommandSets, "custom_commands": patch.CustomCommands}, reversible: true,
			undo: "update the profile again with the before command_sets and custom_commands", limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Endpoints.UpdateProfile(ctx, id, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := s.EndpointProfiles(ctx)
				for _, x := range now {
					if string(x.ID) == id {
						ok := (patch.CustomCommands == nil || equalStrings(x.CustomCommands, patch.CustomCommands)) && (patch.CommandSets == nil || equalStrings(x.CommandSets, patch.CommandSets))
						return ok, map[string]any{"command_sets": x.CommandSets, "custom_commands": x.CustomCommands}, err
					}
				}
				return false, nil, err
			}}, nil
	case "delete":
		p := find(in.Name)
		if p == nil {
			return nil, fmt.Errorf("%w: no endpoint profile %q (inspect-collection view config lists them)", ErrInvalidInput, in.Name)
		}
		before, _ := fwd.Generic(*p)
		id := string(p.ID)
		return &networkPlan{target: "delete endpoint profile " + id, action: "delete_endpoint_profile", before: before, reversible: false, confirm: id,
			limits: []string{"Forward refuses while any endpoint of the organization uses the profile; reassign them first. Its stored connectivity test results go with it"},
			do:     func(ctx context.Context) error { _, err := s.Client.Endpoints.DeleteProfile(ctx, id); return err },
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := s.EndpointProfiles(ctx)
				for _, x := range now {
					if string(x.ID) == id {
						return false, x, err
					}
				}
				return true, nil, err
			}}, nil
	}
	return nil, fmt.Errorf("%w: endpoint_profiles takes action create, update or delete", ErrInvalidInput)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// richProfileDef is the definition of an SNMP or HTTP profile (snake_case keys). On update the given fields replace the profile's current ones.
type richProfileDef struct {
	Name                 *string                `json:"name"`
	Type                 string                 `json:"type"`
	DetectorPatterns     []string               `json:"detector_patterns"`
	NameDetectorPatterns []string               `json:"name_detector_patterns"`
	ResponseTimeoutSec   *int                   `json:"response_timeout_sec"`
	DetectorOID          *string                `json:"detector_oid"`
	NameDetectorOID      *string                `json:"name_detector_oid"`
	OIDSets              []string               `json:"oid_sets"`
	CustomOIDs           []forward.CustomOID    `json:"custom_oids"`
	HTTPS                *bool                  `json:"https"`
	AuthType             *string                `json:"auth_type"`
	Headers              map[string]string      `json:"headers"`
	DetectorURI          *string                `json:"detector_uri"`
	Endpoints            []forward.HTTPEndpoint `json:"endpoints"`
}

const richProfileFields = "name, type (SNMP or HTTP); SNMP: detector_oid, detector_patterns, name_detector_oid, name_detector_patterns, oid_sets, custom_oids [{name, oid}], response_timeout_sec; HTTP: https, auth_type (NONE or BASIC_AUTH), headers, detector_uri, detector_patterns, endpoints [{name, path}]"

// planRichEndpointProfile handles create for type SNMP or HTTP and update of an SNMP or HTTP profile. handled is false for CLI and delete, which planEndpointProfile keeps.
func planRichEndpointProfile(ctx context.Context, s *fwd.Session, in editPlatformInput, profiles []forward.EndpointProfile, find func(string) *forward.EndpointProfile) (plan *networkPlan, handled bool, err error) {
	orgLimits := []string{"a profile is ORGANIZATION-wide: every network sees it; only the endpoints assigned to it (edit-source object endpoint) use it",
		"the profile is saved, not tried: whether it detects and collects a real endpoint is only known after a connectivity test of an endpoint that uses it (inspect-collection view connectivity)"}
	switch in.Action {
	case "create":
		var def richProfileDef
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(in.Definition, &probe) != nil {
			return nil, false, nil
		}
		typ := strings.ToUpper(strings.TrimSpace(probe.Type))
		if typ != "SNMP" && typ != "HTTP" {
			return nil, false, nil
		}
		if err := decodeDefinition(in.Definition, &def, richProfileFields); err != nil {
			return nil, true, err
		}
		if def.Name == nil || strings.TrimSpace(*def.Name) == "" {
			return nil, true, fmt.Errorf("%w: create needs a name (top-level name, or definition.name)", ErrInvalidInput)
		}
		name := strings.TrimSpace(*def.Name)
		if find(name) != nil {
			return nil, true, fmt.Errorf("%w: an endpoint profile named %q already exists (names are unique ignoring case)", ErrInvalidInput, name)
		}
		p := forward.EndpointProfile{Name: name, Type: typ, DetectorPatterns: def.DetectorPatterns, NameDetectorPatterns: def.NameDetectorPatterns, ResponseTimeoutSec: def.ResponseTimeoutSec}
		if typ == "SNMP" {
			if def.DetectorOID == nil || strings.TrimSpace(*def.DetectorOID) == "" || len(def.DetectorPatterns) == 0 {
				return nil, true, fmt.Errorf("%w: an SNMP profile needs detector_oid and detector_patterns (the OID Forward reads to recognise the device, and the patterns its value must match)", ErrInvalidInput)
			}
			p.DetectorOID, p.OIDSets, p.CustomOIDs = *def.DetectorOID, def.OIDSets, def.CustomOIDs
			if def.NameDetectorOID != nil {
				p.NameDetectorOID = *def.NameDetectorOID
			}
			for _, o := range def.CustomOIDs {
				if strings.TrimSpace(o.Name) == "" || strings.TrimSpace(o.OID) == "" {
					return nil, true, fmt.Errorf("%w: each custom_oids entry needs name and oid", ErrInvalidInput)
				}
			}
		} else {
			if def.HTTPS == nil || def.AuthType == nil || len(def.Endpoints) == 0 {
				return nil, true, fmt.Errorf("%w: an HTTP profile needs https, auth_type (NONE or BASIC_AUTH) and at least one endpoint {name, path}", ErrInvalidInput)
			}
			if a := strings.ToUpper(*def.AuthType); a != "NONE" && a != "BASIC_AUTH" {
				return nil, true, fmt.Errorf("%w: auth_type is NONE or BASIC_AUTH", ErrInvalidInput)
			}
			p.HTTPS, p.AuthType, p.Headers, p.Endpoints = def.HTTPS, strings.ToUpper(*def.AuthType), def.Headers, def.Endpoints
			if def.DetectorURI != nil {
				p.DetectorURI = *def.DetectorURI
			}
		}
		limits := append([]string{}, orgLimits...)
		return &networkPlan{target: "create " + typ + " endpoint profile " + name, action: "create_endpoint_profile", after: p, reversible: true,
			undo: "delete the profile (action delete, name its id, confirm its id) after reassigning its endpoints", limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Endpoints.CreateProfileDefinition(ctx, p)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := s.EndpointProfiles(ctx)
				for _, x := range now {
					if strings.EqualFold(x.Name, name) {
						return true, map[string]any{"id": x.ID, "name": x.Name, "type": x.Type}, err
					}
				}
				return false, len(now), err
			}}, true, nil
	case "update":
		p := find(in.Name)
		if p == nil || (p.Type != "SNMP" && p.Type != "HTTP") {
			return nil, false, nil
		}
		var def richProfileDef
		if err := decodeDefinition(in.Definition, &def, "the fields of its type, each replacing the profile's current value: "+richProfileFields); err != nil {
			return nil, true, err
		}
		patch := forward.EndpointProfilePatch{Name: def.Name, DetectorPatterns: def.DetectorPatterns, NameDetectorPatterns: def.NameDetectorPatterns, ResponseTimeoutSec: def.ResponseTimeoutSec}
		if p.Type == "SNMP" {
			if def.HTTPS != nil || def.AuthType != nil || def.Headers != nil || def.DetectorURI != nil || def.Endpoints != nil {
				return nil, true, fmt.Errorf("%w: %s is an SNMP profile; give detector_oid, name_detector_oid, oid_sets, custom_oids, detector_patterns, name_detector_patterns or response_timeout_sec", ErrInvalidInput, p.ID)
			}
			patch.DetectorOID, patch.NameDetectorOID, patch.OIDSets, patch.CustomOIDs = def.DetectorOID, def.NameDetectorOID, def.OIDSets, def.CustomOIDs
		} else {
			if def.DetectorOID != nil || def.NameDetectorOID != nil || def.OIDSets != nil || def.CustomOIDs != nil {
				return nil, true, fmt.Errorf("%w: %s is an HTTP profile; give https, auth_type, headers, detector_uri, detector_patterns or endpoints", ErrInvalidInput, p.ID)
			}
			patch.HTTPS, patch.AuthType, patch.Headers, patch.DetectorURI, patch.Endpoints = def.HTTPS, def.AuthType, def.Headers, def.DetectorURI, def.Endpoints
		}
		given := map[string]any{}
		raw, _ := json.Marshal(def)
		_ = json.Unmarshal(raw, &given)
		for k, v := range given {
			if v == nil {
				delete(given, k)
			}
		}
		if len(given) == 0 {
			return nil, true, fmt.Errorf("%w: update needs at least one field to change", ErrInvalidInput)
		}
		before, _ := fwd.Generic(*p)
		id := string(p.ID)
		limits := append([]string{"the change applies to every endpoint that uses this profile, in every network of the organization, from its next collection"}, orgLimits[1:]...)
		return &networkPlan{target: "update " + p.Type + " endpoint profile " + id, action: "update_endpoint_profile", before: before, after: given, reversible: true,
			undo: "update the profile again with the before values", limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Endpoints.UpdateProfile(ctx, id, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := s.EndpointProfiles(ctx)
				for _, x := range now {
					if string(x.ID) == id {
						got, _ := fwd.Generic(x)
						return true, got, err
					}
				}
				return false, nil, err
			}}, true, nil
	}
	return nil, false, nil
}

// withProfileName puts the top-level name into a create definition (update and delete take the name at the top level, so create accepts it there too). A name in both must agree.
func withProfileName(def json.RawMessage, name string) (json.RawMessage, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return def, nil
	}
	m := map[string]json.RawMessage{}
	if len(def) > 0 {
		if err := json.Unmarshal(def, &m); err != nil {
			return def, nil // decodeDefinition reports the malformed definition
		}
	}
	if raw, ok := m["name"]; ok {
		var inner string
		if json.Unmarshal(raw, &inner) == nil && strings.TrimSpace(inner) != "" && !strings.EqualFold(strings.TrimSpace(inner), name) {
			return nil, fmt.Errorf("%w: name %q and definition.name %q must agree (give one)", ErrInvalidInput, name, inner)
		}
	}
	b, _ := json.Marshal(name)
	m["name"] = b
	return json.Marshal(m)
}
