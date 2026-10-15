package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// planEndpoint manages a network's endpoints: the hosts and devices Forward collects that are not classic network devices (a Linux host over SSH, an SNMP or HTTP device). A create is idempotent by name:
// the same definition again is reported as already done, a different one is refused (use update). Credentials, profiles and jump servers are referred to by id.
func planEndpoint(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, err := s.Endpoints(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.Endpoint {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	type epDef struct {
		Type          string `json:"type"`
		Name          string `json:"name"`
		Host          string `json:"host"`
		Port          int    `json:"port"`
		Protocol      string `json:"protocol"`
		CredentialID  string `json:"credentialId"`
		ProfileID     string `json:"profileId"`
		JumpServerID  string `json:"jumpServerId"`
		Collect       *bool  `json:"collect"`
		CollectorID   string `json:"collectorId"`
		Note          string `json:"note"`
		FullLog       *bool  `json:"fullCollectionLog"`
		LargeRTT      *bool  `json:"largeRtt"`
		NoSSLValidate *bool  `json:"disableSslValidation"`
	}
	fields := "type (CLI, SNMP or HTTP; default CLI), name, host, port, protocol (CLI: SSH or TELNET), credentialId, profileId (the endpoint profile id), jumpServerId (CLI), collect, collectorId, note, fullCollectionLog (CLI, SNMP), largeRtt (CLI), disableSslValidation (HTTP)"
	listed := func(name string) func(ctx context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, err := s.Endpoints(ctx, in.NetworkID)
			for _, e := range l {
				if e.Name == name {
					return true, map[string]any{"name": e.Name, "host": e.Host, "type": e.Type}, err
				}
			}
			return false, len(l), err
		}
	}
	switch in.Action {
	case "create":
		var def epDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		typ := strings.ToUpper(strings.TrimSpace(def.Type))
		if typ == "" {
			typ = "CLI"
		}
		if typ != "CLI" && typ != "SNMP" && typ != "HTTP" {
			return nil, fmt.Errorf("%w: type %q is not one of CLI, SNMP, HTTP", ErrInvalidInput, def.Type)
		}
		if strings.TrimSpace(def.Name) == "" || strings.TrimSpace(def.Host) == "" {
			return nil, fmt.Errorf("%w: create needs name and host", ErrInvalidInput)
		}
		proto := strings.ToUpper(strings.TrimSpace(def.Protocol))
		if proto != "" && typ != "CLI" {
			return nil, fmt.Errorf("%w: protocol is only for a CLI endpoint", ErrInvalidInput)
		}
		if proto != "" && proto != "SSH" && proto != "TELNET" {
			return nil, fmt.Errorf("%w: protocol %q is not one of SSH, TELNET", ErrInvalidInput, def.Protocol)
		}
		ep := forward.Endpoint{Type: typ, Name: def.Name, Host: def.Host, Port: def.Port, Protocol: proto, CredentialID: def.CredentialID, ProfileID: def.ProfileID, JumpServerID: def.JumpServerID,
			Collect: def.Collect, CollectorID: forward.Identifier(def.CollectorID), Note: def.Note, DisableSSLValidation: def.NoSSLValidate}
		if def.FullLog != nil {
			ep.FullCollect = *def.FullLog
		}
		if def.LargeRTT != nil {
			ep.LargeRTT = *def.LargeRTT
		}
		if have := find(def.Name); have != nil {
			same := have.Host == def.Host && (def.CredentialID == "" || have.CredentialID == def.CredentialID) && (def.ProfileID == "" || have.ProfileID == def.ProfileID) && strings.EqualFold(have.Type, typ)
			if !same {
				return nil, fmt.Errorf("%w: an endpoint named %q already exists with a different host, type, credential or profile; use action update, or delete it first", ErrInvalidInput, def.Name)
			}
			return &networkPlan{target: "endpoint " + def.Name, before: map[string]any{"name": have.Name, "host": have.Host, "type": have.Type}, noop: fmt.Sprintf("endpoint %q exists with that host, credential and profile", def.Name)}, nil
		}
		return &networkPlan{target: fmt.Sprintf("add %s endpoint %q (%s) to network %s", typ, def.Name, def.Host, in.NetworkID), action: "add_endpoint", after: def, reversible: true,
			undo:   "delete it (edit-source object endpoint action delete, confirm its name)",
			limits: []string{"credentials, profiles and jump servers are referred to by id (inspect-platform area credentials; inspect-collection view config lists the endpoint profiles); it is collected from the next collection"},
			do: func(ctx context.Context) error {
				_, err := s.Client.Endpoints.AddBatch(ctx, in.NetworkID, typ, []forward.Endpoint{ep})
				return err
			},
			verify: listed(def.Name)}, nil
	case "update":
		var def epDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		have := find(in.Name)
		if have == nil {
			return nil, nil
		}
		if def.Name != "" || def.Type != "" || def.Port != 0 || def.Note != "" || def.FullLog != nil || def.LargeRTT != nil || def.NoSSLValidate != nil {
			return nil, fmt.Errorf("%w: update can change host, protocol, credentialId, profileId, jumpServerId, collect and collectorId", ErrInvalidInput)
		}
		patch := forward.EndpointPatch{Collect: def.Collect}
		set := func(v string, to **string) {
			if v != "" {
				x := v
				*to = &x
			}
		}
		set(def.Host, &patch.Host)
		set(strings.ToUpper(def.Protocol), &patch.Protocol)
		set(def.CredentialID, &patch.CredentialID)
		set(def.ProfileID, &patch.ProfileID)
		set(def.JumpServerID, &patch.JumpServerID)
		set(def.CollectorID, &patch.CollectorID)
		before, _ := fwd.Generic(have)
		return &networkPlan{target: fmt.Sprintf("update endpoint %q on network %s", in.Name, in.NetworkID), action: "update_endpoint", before: before, after: def, reversible: true, undo: "update it back with the before values",
			do: func(ctx context.Context) error {
				_, err := s.Client.Endpoints.Patch(ctx, in.NetworkID, in.Name, have.Type, patch)
				return err
			}}, nil
	case "delete":
		if in.Name == "" || len(in.Definition) > 0 {
			return nil, fmt.Errorf("%w: delete takes name (the endpoint) and confirm only", ErrInvalidInput)
		}
		have := find(in.Name)
		if have == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(have)
		name := in.Name
		return &networkPlan{target: fmt.Sprintf("delete endpoint %q from network %s", name, in.NetworkID), action: "delete_endpoint", before: before, reversible: true, confirm: name,
			undo:   "add it again with the before values (edit-source object endpoint action create)",
			limits: []string{"removing an endpoint stops it being collected; past snapshots keep it"},
			do: func(ctx context.Context) error {
				_, err := s.Client.Endpoints.Delete(ctx, in.NetworkID, name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				ok, got, err := listed(name)(ctx)
				return !ok, got, err
			}}, nil
	}
	return nil, fmt.Errorf("%w: object endpoint takes action create, update or delete", ErrInvalidInput)
}
