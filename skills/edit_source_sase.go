package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// planSase creates, changes, deletes or tests a Zscaler SASE setup of the network (Preview routes, read from Forward's source through the SDK). The ZPA
// login is an HTTP LOGIN credential that already exists (httpCredId) and a SCIM endpoint's an API_KEY one, so no secret is read here. A test only queues
// a collector task; its result lands on the setup later (inspect-platform area sase_setups).
func planSase(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	var def struct {
		Name               string                       `json:"name"`
		BaseURL            *string                      `json:"baseUrl"`
		HTTPCredID         *string                      `json:"httpCredId"`
		CollectorID        *string                      `json:"collectorId"`
		Collect            *bool                        `json:"collect"`
		AppConnectorSetups *[]forward.AppConnectorSetup `json:"appConnectorSetups"`
		ScimConfigs        *[]forward.ScimConfig        `json:"scimConfigs"`
		Connectors         bool                         `json:"connectors"`
		ConnectorID        string                       `json:"connectorId"`
	}
	if in.Action != "delete" {
		if err := decodeDefinition(in.Definition, &def, "name, baseUrl (the ZPA API base), httpCredId (an HTTP LOGIN credential: client id and secret), collectorId, collect, appConnectorSetups [{connectorId, name, host, collectionType SSH|MANUAL, port, credentialId, jumpServerId, locationId, collect}], scimConfigs [{url, httpCredId (API_KEY)}]; for test: name, and connectors: true or connectorId"); err != nil {
			return nil, err
		}
	}
	def.Name = strings.ToLower(strings.TrimSpace(def.Name))
	target := strings.ToLower(strings.TrimSpace(firstNonEmpty(in.Name, def.Name)))
	get := func(ctx context.Context, name string) (*forward.ZscalerSetup, error) {
		x, _, err := s.Client.SaseSetups.Get(ctx, in.NetworkID, name)
		if err != nil && forward.IsStatus(err, 404) {
			return nil, nil
		}
		return x, err
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	shown := func(name string, want func(forward.ZscalerSetup) bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			x, err := get(ctx, name)
			if err != nil || x == nil {
				return false, nil, err
			}
			g, _ := fwd.Generic(*x)
			return want(*x), g, nil
		}
	}
	notTested := "saving a setup does not prove the tenant accepts the credential: Forward queues a test after a create, and action test queues one at any time; read the result with inspect-platform area sase_setups"
	switch in.Action {
	case "create":
		if def.Name == "" || def.BaseURL == nil || def.HTTPCredID == nil {
			return nil, fmt.Errorf("%w: create needs name, baseUrl and httpCredId (an HTTP LOGIN credential from inspect-platform area credentials)", ErrInvalidInput)
		}
		if cur, err := get(ctx, def.Name); err != nil {
			return nil, err
		} else if cur != nil {
			return nil, fmt.Errorf("%w: a SASE setup named %q already exists: use action update", ErrInvalidInput, def.Name)
		}
		setup := forward.ZscalerSetup{Name: def.Name, BaseURL: str(def.BaseURL), HTTPCredID: str(def.HTTPCredID), CollectorID: forward.Identifier(str(def.CollectorID)), Collect: def.Collect}
		if def.AppConnectorSetups != nil {
			setup.AppConnectorSetups = *def.AppConnectorSetups
		}
		if def.ScimConfigs != nil {
			setup.ScimConfigs = *def.ScimConfigs
		}
		return &networkPlan{target: fmt.Sprintf("create SASE setup %q on network %s", def.Name, in.NetworkID), action: "create_sase_setup", after: setup, reversible: true,
			undo:   "delete the setup (object sase_setup action delete; the credentials it uses are not deleted)",
			limits: []string{notTested, "Forward saves the setup first and then queues its tests: with no online collector it answers 400 although the setup now exists, so a failed create is read back before it is reported"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.SaseSetups.Create(ctx, in.NetworkID, setup)
				if err != nil {
					if x, gerr := get(ctx, def.Name); gerr == nil && x != nil {
						return nil // saved; only the queued test failed (no online collector): the read-back proves the setup
					}
				}
				return err
			},
			verify: shown(def.Name, func(x forward.ZscalerSetup) bool { return x.Name == def.Name })}, nil
	case "update":
		x, err := get(ctx, target)
		if err != nil {
			return nil, err
		}
		if x == nil {
			return nil, nil
		}
		patch := forward.ZscalerSetupPatch{BaseURL: def.BaseURL, HTTPCredID: def.HTTPCredID, CollectorID: def.CollectorID, Collect: def.Collect, ScimConfigs: def.ScimConfigs}
		if def.Name != "" && def.Name != x.Name && in.Name != "" {
			n := def.Name
			patch.Name = &n
		}
		var limits []string
		if def.AppConnectorSetups != nil {
			// the list is replaced whole and Forward stores each connector's test result as sent: carry the saved result over for a connector that stays
			saved := map[string]*forward.ConnectivityResult{}
			for _, c := range x.AppConnectorSetups {
				saved[c.ConnectorID] = c.TestResult
			}
			list := append([]forward.AppConnectorSetup(nil), (*def.AppConnectorSetups)...)
			for i := range list {
				if list[i].TestResult == nil {
					list[i].TestResult = saved[list[i].ConnectorID]
				}
			}
			patch.AppConnectorSetups = &list
			limits = append(limits, fmt.Sprintf("appConnectorSetups REPLACES the whole connector list (%d now, %d after): a connector left out is removed; the saved test result of a connector that stays is carried over", len(x.AppConnectorSetups), len(list)))
		}
		if def.ScimConfigs != nil {
			limits = append(limits, "scimConfigs replaces the whole SCIM list")
		}
		if patch.Name == nil && patch.BaseURL == nil && patch.HTTPCredID == nil && patch.CollectorID == nil && patch.Collect == nil && patch.AppConnectorSetups == nil && patch.ScimConfigs == nil {
			return nil, fmt.Errorf("%w: update changes only what is given: name (with the setup to change in name at the top level), baseUrl, httpCredId, collectorId, collect, appConnectorSetups, scimConfigs", ErrInvalidInput)
		}
		limits = append(limits, "httpCredId, collectorId and collect cannot be cleared here (the SDK cannot send the null Forward reads as clear): set them to another value, or change them in the Forward UI", notTested)
		before, _ := fwd.Generic(*x)
		final := x.Name
		if patch.Name != nil {
			final = *patch.Name
		}
		return &networkPlan{target: fmt.Sprintf("change SASE setup %q on network %s", x.Name, in.NetworkID), action: "update_sase_setup", before: before, after: patch, reversible: true,
			undo: "update again with the before values (connectors and SCIM endpoints as listed in before)", limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.SaseSetups.Patch(ctx, in.NetworkID, x.Name, patch)
				return err
			},
			verify: shown(final, func(y forward.ZscalerSetup) bool {
				return (patch.BaseURL == nil || strings.Contains(*patch.BaseURL, hostOf(y.BaseURL))) && (patch.AppConnectorSetups == nil || len(y.AppConnectorSetups) == len(*patch.AppConnectorSetups))
			})}, nil
	case "delete":
		x, err := get(ctx, target)
		if err != nil {
			return nil, err
		}
		if x == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(*x)
		return &networkPlan{target: fmt.Sprintf("delete SASE setup %q from network %s", x.Name, in.NetworkID), action: "delete_sase_setup", before: before, reversible: false, confirm: x.Name,
			undo:   "none: create it again from the before values (its saved test results are lost; the credentials it used are not deleted)",
			limits: []string{fmt.Sprintf("the setup's %d App Connector(s) stop being collected from the next collection", len(x.AppConnectorSetups))},
			do: func(ctx context.Context) error {
				_, err := s.Client.SaseSetups.Delete(ctx, in.NetworkID, x.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				y, err := get(ctx, x.Name)
				return err == nil && y == nil, nil, err
			}}, nil
	case "test":
		return planSaseTest(ctx, s, in, def.Name, def.Connectors, def.ConnectorID)
	}
	return nil, fmt.Errorf("%w: object sase_setup takes action create, update, delete or test (name at the top level is the setup to update or delete)", ErrInvalidInput)
}

// hostOf is the host part of a URL (Forward keeps only the host of baseUrl).
func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexAny(u, "/:"); i >= 0 {
		u = u[:i]
	}
	return u
}

// planSaseTest queues a test of the tenant, every App Connector, or one; it changes no setting.
func planSaseTest(ctx context.Context, s *fwd.Session, in editSourceInput, name string, connectors bool, connectorID string) (*networkPlan, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: test needs definition.name, the SASE setup", ErrInvalidInput)
	}
	if connectors && connectorID != "" {
		return nil, fmt.Errorf("%w: give connectors: true or connectorId, not both", ErrInvalidInput)
	}
	setup, _, err := s.Client.SaseSetups.Get(ctx, in.NetworkID, name)
	if err != nil {
		return nil, err
	}
	what, call := "the Zscaler tenant", func(ctx context.Context) error {
		_, e := s.Client.SaseSetups.TestSetup(ctx, in.NetworkID, name)
		return e
	}
	switch {
	case connectors:
		what, call = fmt.Sprintf("its %d App Connector(s)", len(setup.AppConnectorSetups)), func(ctx context.Context) error {
			_, e := s.Client.SaseSetups.TestConnectors(ctx, in.NetworkID, name)
			return e
		}
	case connectorID != "":
		found := false
		for _, c := range setup.AppConnectorSetups {
			found = found || c.ConnectorID == connectorID
		}
		if !found {
			return nil, fmt.Errorf("%w: setup %s lists no connector %s (inspect-platform area sase_setups shows its connectors)", ErrInvalidInput, name, connectorID)
		}
		what, call = "App Connector "+connectorID, func(ctx context.Context) error {
			_, e := s.Client.SaseSetups.TestConnector(ctx, in.NetworkID, name, connectorID)
			return e
		}
	}
	last := ""
	if setup.TestResult != nil {
		last = setup.TestResult.SavedAt
	}
	return &networkPlan{target: fmt.Sprintf("test %s of SASE setup %q in network %s", what, name, in.NetworkID), action: "test_sase_setup",
		after: map[string]any{"setup": name, "tests": what, "previous_test_saved_at": last}, reversible: true,
		undo: "none needed: a test changes no setting, it only stores a new test result on the setup",
		limits: []string{"a test only queues a collector task (Forward answers 204); the result is saved on the setup later: read it with inspect-platform area sase_setups (the setup's test result, or each connector's) and compare its saved time with previous_test_saved_at",
			"it needs an online collector for the network; the error, when there is one, is Forward's enum name (for example AUTHENTICATION_FAILED)"},
		do: call}, nil
}
