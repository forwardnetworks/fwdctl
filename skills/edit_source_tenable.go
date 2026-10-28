package skills

import (
	"context"
	"fmt"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/forwardnetworks/fwdctl/fwd"
)

// planTenable creates, changes, tests or deletes a Tenable Security Center vulnerability source of the network (Forward's end-host scanner of type TENABLE). Its login is
// an HTTP LOGIN credential that already exists (credentialId), so no secret is read here. Unlike Rapid7, update is a patch: only the fields given change.
func planTenable(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	var def struct {
		Name                 string  `json:"name"`
		BaseURL              *string `json:"baseUrl"`
		CredentialID         *string `json:"credentialId"`
		DisableSSLValidation *bool   `json:"disableSslValidation"`
		CollectionDisabled   *bool   `json:"collectionDisabled"`
		MaxPageSize          *int    `json:"maxPageSize"`
		ClearMaxPageSize     bool    `json:"clearMaxPageSize"`
		CollectorID          *string `json:"collectorId"`
		ClearCollector       bool    `json:"clearCollector"`
	}
	if in.Action != "delete" {
		if err := decodeDefinition(in.Definition, &def, "name (letters, digits, _ . - only), baseUrl, credentialId (an existing HTTP LOGIN credential), disableSslValidation, collectionDisabled, maxPageSize (unset: Forward's 10000), clearMaxPageSize, collectorId, clearCollector"); err != nil {
			return nil, err
		}
	}
	cur, _, err := s.Client.Integrations.ListTenableSC(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	find := func(name string) *forward.TenableSource {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	flag := func(p *bool) bool { return p != nil && *p }
	create := forward.TenableSourceRequest{Name: def.Name, BaseURL: str(def.BaseURL), CredentialID: forward.Identifier(str(def.CredentialID)), DisableSSLValidation: flag(def.DisableSSLValidation),
		CollectionDisabled: flag(def.CollectionDisabled), MaxPageSize: def.MaxPageSize, CollectorID: forward.Identifier(str(def.CollectorID))}
	shown := func(name string, want func(forward.TenableSource) bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.Integrations.ListTenableSC(ctx, in.NetworkID)
			for _, x := range l {
				if x.Name == name {
					g, _ := fwd.Generic(x)
					return want(x), g, e
				}
			}
			return false, nil, e
		}
	}
	notTested := "this does not test that Tenable Security Center accepts the credential: use action test (it calls the scanner from the collector or appserver), and read collectionError in inspect-platform area integrations after the next collection"
	switch in.Action {
	case "create":
		if def.Name == "" || def.BaseURL == nil || def.CredentialID == nil {
			return nil, fmt.Errorf("%w: create needs name, baseUrl and credentialId (an HTTP LOGIN credential from inspect-platform area credentials)", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a Tenable source named %q already exists", ErrInvalidInput, def.Name)
		}
		return &networkPlan{target: fmt.Sprintf("create Tenable Security Center source %q on network %s", def.Name, in.NetworkID), action: "create_tenable_source", after: create, reversible: true,
			undo:   "delete the source (the credential it used is not deleted)",
			limits: []string{notTested},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Integrations.CreateTenableSC(ctx, in.NetworkID, create)
				return err
			},
			verify: shown(def.Name, func(x forward.TenableSource) bool {
				return x.BaseURL == create.BaseURL && x.CollectionDisabled == create.CollectionDisabled
			})}, nil
	case "update":
		x := find(in.Name)
		if x == nil {
			return nil, nil
		}
		patch := forward.TenableSourcePatch{BaseURL: def.BaseURL, DisableSSLValidation: def.DisableSSLValidation, CollectionDisabled: def.CollectionDisabled, MaxPageSize: def.MaxPageSize,
			ClearMaxPageSize: def.ClearMaxPageSize, ClearCollector: def.ClearCollector}
		if def.Name != "" && def.Name != x.Name {
			n := def.Name
			patch.Name = &n
		}
		if def.CredentialID != nil {
			id := forward.Identifier(*def.CredentialID)
			patch.CredentialID = &id
		}
		if def.CollectorID != nil {
			id := forward.Identifier(*def.CollectorID)
			patch.CollectorID = &id
		}
		if patch.Name == nil && patch.BaseURL == nil && patch.CredentialID == nil && patch.DisableSSLValidation == nil && patch.CollectionDisabled == nil && patch.MaxPageSize == nil &&
			patch.CollectorID == nil && !patch.ClearMaxPageSize && !patch.ClearCollector {
			return nil, fmt.Errorf("%w: update changes only what is given: name, baseUrl, credentialId, disableSslValidation, collectionDisabled, maxPageSize, clearMaxPageSize, collectorId or clearCollector", ErrInvalidInput)
		}
		before, _ := fwd.Generic(*x)
		final := x.Name
		if patch.Name != nil {
			final = *patch.Name
		}
		return &networkPlan{target: fmt.Sprintf("change Tenable Security Center source %q on network %s", x.Name, in.NetworkID), action: "update_tenable_source", before: before, after: patch, reversible: true,
			undo:   "update again with the before values",
			limits: []string{"only the fields given change; the others stay as they are", notTested},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Integrations.UpdateTenableSC(ctx, in.NetworkID, in.Name, patch)
				return err
			},
			verify: shown(final, func(y forward.TenableSource) bool {
				return (patch.BaseURL == nil || y.BaseURL == *patch.BaseURL) && (patch.CollectionDisabled == nil || y.CollectionDisabled == *patch.CollectionDisabled)
			})}, nil
	case "test":
		if def.Name == "" || def.BaseURL == nil || def.CredentialID == nil {
			return nil, fmt.Errorf("%w: test needs name, baseUrl and credentialId (and collectorId if one collects it)", ErrInvalidInput)
		}
		probe := forward.TenableSourceTest{Name: def.Name, BaseURL: str(def.BaseURL), CredentialID: forward.Identifier(str(def.CredentialID)), DisableSSLValidation: flag(def.DisableSSLValidation), CollectorID: forward.Identifier(str(def.CollectorID))}
		return &networkPlan{target: fmt.Sprintf("test the connection to Tenable Security Center %q for network %s", def.Name, in.NetworkID), action: "test_tenable_source", after: probe, reversible: true,
			undo:   "none needed: a test stores nothing",
			limits: []string{"the test sends a login to the scanner address in baseUrl; it creates and changes no source"},
			do: func(ctx context.Context) error {
				r, _, err := s.Client.Integrations.TestTenableSC(ctx, in.NetworkID, probe)
				if err != nil {
					return err
				}
				if !r.OK() {
					return fmt.Errorf("Tenable Security Center refused the connection test: %s", r.Error)
				}
				return nil
			}}, nil
	case "delete":
		x := find(in.Name)
		if x == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(*x)
		return &networkPlan{target: fmt.Sprintf("delete Tenable Security Center source %q from network %s", x.Name, in.NetworkID), action: "delete_tenable_source", before: before, reversible: false, confirm: x.Name,
			undo:   "none: create it again from the before values (the credential it used is not deleted); Forward answers deleted for a wrong network id too, so the read-back checks it is gone",
			limits: []string{"the host severity counts this source supplied leave the next snapshot's exposure view"},
			do: func(ctx context.Context) error {
				_, err := s.Client.Integrations.DeleteTenableSC(ctx, in.NetworkID, x.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.Integrations.ListTenableSC(ctx, in.NetworkID)
				for _, y := range l {
					if y.Name == x.Name {
						return false, y, e
					}
				}
				return true, nil, e
			}}, nil
	}
	return nil, fmt.Errorf("%w: object tenable_source takes action create, update, test or delete (name is the source to update or delete)", ErrInvalidInput)
}
