package skills

import (
	"context"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// planSase tests a Zscaler SASE setup of the network: the tenant, its App Connectors, or one connector. A test only queues a collector task
// (Forward answers 204); the result lands on the setup later, so the change says to read it with inspect-platform area sase_setups. It
// creates, changes and deletes nothing. (Create, change and delete are not offered here.)
func planSase(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	if in.Action != "test" {
		return nil, fmt.Errorf("%w: object sase_setup takes action test only (the tenant, its connectors, or one connector); read setups with inspect-platform area sase_setups", ErrInvalidInput)
	}
	var def struct {
		Name        string `json:"name"`
		Connectors  bool   `json:"connectors"`
		ConnectorID string `json:"connectorId"`
	}
	if err := decodeDefinition(in.Definition, &def, "name (the setup), and optionally connectors: true (test every App Connector) or connectorId (test one)"); err != nil {
		return nil, err
	}
	def.Name = strings.ToLower(strings.TrimSpace(def.Name))
	if def.Name == "" {
		return nil, fmt.Errorf("%w: test needs definition.name, the SASE setup", ErrInvalidInput)
	}
	if def.Connectors && def.ConnectorID != "" {
		return nil, fmt.Errorf("%w: give connectors: true or connectorId, not both", ErrInvalidInput)
	}
	setup, _, err := s.Client.SaseSetups.Get(ctx, in.NetworkID, def.Name)
	if err != nil {
		return nil, err
	}
	what, call := "the Zscaler tenant", func(ctx context.Context) error {
		_, e := s.Client.SaseSetups.TestSetup(ctx, in.NetworkID, def.Name)
		return e
	}
	switch {
	case def.Connectors:
		what, call = fmt.Sprintf("its %d App Connector(s)", len(setup.AppConnectorSetups)), func(ctx context.Context) error {
			_, e := s.Client.SaseSetups.TestConnectors(ctx, in.NetworkID, def.Name)
			return e
		}
	case def.ConnectorID != "":
		found := false
		for _, c := range setup.AppConnectorSetups {
			found = found || c.ConnectorID == def.ConnectorID
		}
		if !found {
			return nil, fmt.Errorf("%w: setup %s lists no connector %s (inspect-platform area sase_setups shows its connectors)", ErrInvalidInput, def.Name, def.ConnectorID)
		}
		what, call = "App Connector "+def.ConnectorID, func(ctx context.Context) error {
			_, e := s.Client.SaseSetups.TestConnector(ctx, in.NetworkID, def.Name, def.ConnectorID)
			return e
		}
	}
	last := ""
	if setup.TestResult != nil {
		last = setup.TestResult.SavedAt
	}
	return &networkPlan{target: fmt.Sprintf("test %s of SASE setup %q in network %s", what, def.Name, in.NetworkID), action: "test_sase_setup",
		after: map[string]any{"setup": def.Name, "tests": what, "previous_test_saved_at": last}, reversible: true,
		undo: "none needed: a test changes no setting, it only stores a new test result on the setup",
		limits: []string{"a test only queues a collector task (Forward answers 204); the result is saved on the setup later: read it with inspect-platform area sase_setups (the setup's test result, or each connector's) and compare its saved time with previous_test_saved_at",
			"it needs an online collector for the network; the error, when there is one, is Forward's enum name (for example AUTHENTICATION_FAILED)"},
		do: call}, nil
}
