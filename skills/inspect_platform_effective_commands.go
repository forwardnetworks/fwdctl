package skills

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// effectiveCustomCommands is inspect-platform area custom_command_groups, view effective: the custom command groups that apply to ONE classic device. A group applies when it is enabled, its device types include the device's
// type (the configured type, else the one its last connectivity test discovered) and its devices list is empty or one of its names or globs matches the device name (ignoring case). Built-in commands are not part of it.
func effectiveCustomCommands(ctx context.Context, s *fwd.Session, networkID, device string, cx result.Context) (result.Result, error) {
	if strings.TrimSpace(device) == "" {
		return result.Result{}, fmt.Errorf("%w: view effective of custom_command_groups needs name, the classic device", ErrInvalidInput)
	}
	groups, _, err := s.Client.CustomCommandGroups.List(ctx, networkID)
	if err != nil {
		if r, ok := denialResult(inspectPlatformName, err, cx, "could not read the custom command groups"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	devs, err := s.ClassicDevices(ctx, networkID)
	if err != nil {
		return result.Result{}, err
	}
	typ := ""
	found := false
	for _, d := range devs {
		if strings.EqualFold(d.Name, device) {
			typ, found = d.Type, true
			device = d.Name
		}
	}
	if !found {
		return result.NewUnknown(inspectPlatformName, fmt.Sprintf("%q is not a classic device of this network", device), cx, []string{"nothing was read for it; a cloud or controller-managed device is not covered by custom command groups here"}, result.Options{})
	}
	var limits []string
	if typ == "" {
		if sts, serr := s.DeviceTestStatuses(ctx, networkID); serr == nil {
			for _, st := range sts {
				if strings.EqualFold(st.Name, device) && st.DiscoveredType != "" && st.DiscoveredType != "unknown" {
					typ = st.DiscoveredType
				}
			}
		}
	}
	if typ == "" {
		limits = append(limits, "the device has no configured type and its test discovered none, so a group is matched by name alone and the device types are not checked")
	}
	var rows []map[string]any
	for _, g := range groups {
		typeOK := typ == ""
		for _, t := range g.DeviceConnTypes {
			if t == typ {
				typeOK = true
			}
		}
		nameOK := len(g.Devices) == 0
		for _, pat := range g.Devices {
			if ok, _ := path.Match(strings.ToLower(pat), strings.ToLower(device)); ok || strings.EqualFold(pat, device) {
				nameOK = true
			}
		}
		if !typeOK || !nameOK {
			continue
		}
		row := map[string]any{"name": g.Name, "type": g.Type, "device_types": g.DeviceConnTypes, "disabled": g.Disabled, "applies": !g.Disabled}
		if g.Type == "SNMP" || len(g.OIDs) > 0 {
			row["oids"] = len(g.OIDs)
		} else {
			cmds, n := result.CapRow(g.Commands, 100)
			row["commands"], row["commands_total"] = cmds, n
		}
		rows = append(rows, row)
	}
	limits = append(limits,
		"this lists the custom command groups of the network that match the device's type and name; built-in commands are not listed, and the org-wide disabled-commands setting can still skip a command in an interactive session",
		"a group's devices list is matched here as names or globs ignoring case (a `*` and `?` glob); Forward's resolve-globs call is the authority if a pattern is unusual",
		"groups that are tagged or limited by something other than device type and name are not modelled by the SDK and are not evaluated here")
	finding := fmt.Sprintf("%d custom command group(s) match %s (type %s)", len(rows), device, orDash(typ))
	if len(rows) == 0 {
		finding = fmt.Sprintf("No custom command group matches %s (type %s)", device, orDash(typ))
	}
	return result.Build(inspectPlatformName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-collection"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "platform_custom_command_groups", nil, map[string]any{"area": "custom_command_groups", "view": "effective", "device": device, "device_type": typ, "rows": rows}, finding)}})
}
