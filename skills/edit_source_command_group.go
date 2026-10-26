package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// planCustomCommandGroup manages a network's custom command groups: the CLI commands (or SNMP OIDs) it adds to a device type's collection, which are not Forward built-ins. Create and update are checked with Forward's own
// validation first (a command the device type refuses is reported in the dry run, before anything is sent). To stop one, update it with disabled true; delete removes it and needs confirm.
func planCustomCommandGroup(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.CustomCommandGroups.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.CustomCommandGroup {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	exists := func(name string, want bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.CustomCommandGroups.List(ctx, in.NetworkID)
			for _, g := range l {
				if g.Name == name {
					return want, nil, e
				}
			}
			return !want, nil, e
		}
	}
	validate := func(types, cmds []string) error {
		if len(cmds) == 0 || len(types) == 0 {
			return nil
		}
		bad, _, verr := s.Client.CustomCommandGroups.Validate(ctx, in.NetworkID, types, cmds)
		if verr != nil {
			return verr
		}
		if len(bad) > 0 {
			var parts []string
			for _, b := range bad {
				parts = append(parts, fmt.Sprintf("%q is refused for %s", b.Command, strings.Join(b.UnsupportedConnTypes, ", ")))
			}
			return fmt.Errorf("%w: Forward's validation refuses: %s", ErrInvalidInput, strings.Join(parts, "; "))
		}
		return nil
	}
	fields := "name, deviceConnTypes, commands (CLI) or oids [{name, oid}] (SNMP), devices (names or globs; empty = every device of the types), disabled"
	switch in.Action {
	case "create":
		var def forward.NewCustomCommandGroup
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" || len(def.DeviceConnTypes) == 0 || (len(def.Commands) == 0) == (len(def.OIDs) == 0) {
			return nil, fmt.Errorf("%w: create needs name, deviceConnTypes and exactly one of commands and oids", ErrInvalidInput)
		}
		for _, g := range cur {
			if strings.EqualFold(g.Name, def.Name) {
				return nil, fmt.Errorf("%w: a custom command group named %q already exists (names are unique ignoring case)", ErrInvalidInput, g.Name)
			}
		}
		if err := validate(def.DeviceConnTypes, def.Commands); err != nil {
			return nil, err
		}
		return &networkPlan{target: fmt.Sprintf("create custom command group %q on network %s", def.Name, in.NetworkID), action: "create_custom_command_group", after: def, reversible: true,
			undo:   "delete the group (action delete, name it, confirm its name)",
			limits: []string{"the commands were checked with Forward's validation for the device types; a device login that cannot run one will fail its connectivity test as CUSTOM_COMMAND_AUTHORIZATION_FAILED", "this is a custom group of the network, not a Forward built-in"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CustomCommandGroups.Create(ctx, in.NetworkID, def)
				return err
			},
			verify: exists(def.Name, true)}, nil
	case "update":
		g := find(in.Name)
		if g == nil {
			return nil, nil
		}
		var def forward.CustomCommandGroupUpdate
		if err := decodeDefinition(in.Definition, &def, fields+" (partial: only what is given changes; commands or oids replace the whole list)"); err != nil {
			return nil, err
		}
		if def.Name == nil && def.DeviceConnTypes == nil && def.Commands == nil && def.OIDs == nil && def.Devices == nil && def.Disabled == nil {
			return nil, fmt.Errorf("%w: update must change at least one field", ErrInvalidInput)
		}
		types := def.DeviceConnTypes
		if types == nil {
			types = g.DeviceConnTypes
		}
		cmds := def.Commands
		if cmds == nil && def.DeviceConnTypes != nil {
			cmds = g.Commands // changing the types re-checks the stored commands against them
		}
		if err := validate(types, cmds); err != nil {
			return nil, err
		}
		before, _ := fwd.Generic(g)
		return &networkPlan{target: fmt.Sprintf("change custom command group %q on network %s", g.Name, in.NetworkID), action: "update_custom_command_group", before: before, after: def, reversible: true,
			undo:   "update again with the before values (to stop a group without deleting it, set disabled true)",
			limits: []string{"disabled keeps the group but stops collecting it; commands and oids replace the whole list", "this is a custom group of the network, not a Forward built-in"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CustomCommandGroups.Update(ctx, in.NetworkID, g.Name, def)
				return err
			}}, nil
	case "delete":
		g := find(in.Name)
		if g == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(g)
		return &networkPlan{target: fmt.Sprintf("delete custom command group %q from network %s", g.Name, in.NetworkID), action: "delete_custom_command_group", before: before, reversible: true, confirm: g.Name,
			undo:   "create the group again from the before values (action create)",
			limits: []string{"its commands are no longer collected on the next collection; past snapshots keep what they collected. To stop it without removing it, update it with disabled true"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CustomCommandGroups.Delete(ctx, in.NetworkID, g.Name)
				return err
			},
			verify: exists(g.Name, false)}, nil
	}
	return nil, fmt.Errorf("%w: custom_command_group takes action create, update or delete", ErrInvalidInput)
}
