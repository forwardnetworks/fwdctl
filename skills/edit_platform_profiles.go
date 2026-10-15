package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// planEndpointProfile creates and deletes CLI endpoint profiles (the commands Forward runs on the endpoints that use one). A profile is organization-wide: every network sees it. SNMP profiles are copied
// by edit-endpoint-profile; assigning endpoints to a profile is edit-source object endpoint.
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
			return nil, fmt.Errorf("%w: only CLI profiles are created here (SNMP profiles are copied by edit-endpoint-profile)", ErrInvalidInput)
		}
		if def.Name == nil || strings.TrimSpace(*def.Name) == "" || len(def.CommandSets)+len(def.CustomCommands) == 0 {
			return nil, fmt.Errorf("%w: create needs name and command_sets or custom_commands", ErrInvalidInput)
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
	return nil, fmt.Errorf("%w: endpoint_profiles takes action create or delete", ErrInvalidInput)
}
