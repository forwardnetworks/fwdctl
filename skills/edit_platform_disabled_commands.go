package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// Default version range for a disabled command: every version. Forward compares dotted numeric versions with zero padding, so "99999" is above any real version.
const (
	allVersionsStart = "0"
	allVersionsEnd   = "99999"
)

type disableEntry struct {
	DeviceType   string `json:"device_type"`
	Command      string `json:"command"`
	VersionStart string `json:"version_start"`
	VersionEnd   string `json:"version_end"`
}

func hasDisabledCommandEdit(raw json.RawMessage) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return false
	}
	_, a := keys["disable_commands"]
	_, b := keys["enable_commands"]
	return a || b
}

// upperClass finds a regex class escape in upper case (\S, \D, \W, ...), which lowercasing turns into the opposite class.
var upperClass = regexp.MustCompile(`\\[A-Z]`)

// planDisabledCommands turns built-in collection commands off org-wide, or back on. Forward replaces the whole map on a patch, so each entry is added or removed by reading the map, changing a copy, and sending
// all of it back, read again right before the write. It is organization-wide and takes effect at the next collection.
func planDisabledCommands(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	var def struct {
		Disable []disableEntry `json:"disable_commands"`
		Enable  []struct {
			DeviceType string `json:"device_type"`
			Command    string `json:"command"`
		} `json:"enable_commands"`
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(in.Definition, &keys)
	for k := range keys {
		if k != "disable_commands" && k != "enable_commands" {
			return nil, fmt.Errorf("%w: disable_commands and enable_commands are changed on their own; %q is a separate set call", ErrInvalidInput, k)
		}
	}
	if err := decodeDefinition(in.Definition, &def, "disable_commands [{device_type, command (a regex matched against the whole lowercased command), version_start, version_end}], enable_commands [{device_type, command}]"); err != nil {
		return nil, err
	}
	var goRegexWarn []string
	for i := range def.Disable {
		e := &def.Disable[i]
		e.DeviceType, e.Command = strings.TrimSpace(e.DeviceType), strings.TrimSpace(e.Command)
		if e.DeviceType == "" || e.Command == "" {
			return nil, fmt.Errorf("%w: each disable_commands entry needs device_type (for example panos_ssh) and command", ErrInvalidInput)
		}
		if _, err := regexp.Compile(e.Command); err != nil {
			return nil, fmt.Errorf("%w: command %q is not a valid regular expression (%v): Forward skips an invalid pattern silently, so it would disable nothing", ErrInvalidInput, e.Command, err)
		}
		if e.VersionStart == "" {
			e.VersionStart = allVersionsStart
		}
		if e.VersionEnd == "" {
			e.VersionEnd = allVersionsEnd
		}
		if e.Command != strings.ToLower(e.Command) {
			goRegexWarn = append(goRegexWarn, e.Command)
		}
	}
	for _, e := range def.Enable {
		if strings.TrimSpace(e.DeviceType) == "" || strings.TrimSpace(e.Command) == "" {
			return nil, fmt.Errorf("%w: each enable_commands entry needs device_type and command (exactly as it was disabled)", ErrInvalidInput)
		}
	}
	if len(def.Disable)+len(def.Enable) == 0 {
		return nil, fmt.Errorf("%w: give disable_commands or enable_commands", ErrInvalidInput)
	}
	read := func(ctx context.Context) (map[string][]forward.DisabledCommand, error) {
		cur, _, err := s.Client.Collectors.GetOrganizationSettings(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string][]forward.DisabledCommand{}
		if cur != nil {
			for k, v := range cur.DisabledCommands {
				out[k] = append([]forward.DisabledCommand(nil), v...)
			}
		}
		return out, nil
	}
	// compose applies the changes to a copy of the map; it reports what changed
	compose := func(cur map[string][]forward.DisabledCommand) (map[string][]forward.DisabledCommand, []string, error) {
		next := map[string][]forward.DisabledCommand{}
		for k, v := range cur {
			next[k] = append([]forward.DisabledCommand(nil), v...)
		}
		var changes []string
		for _, e := range def.Disable {
			want := forward.DisabledCommand{Command: e.Command, Version: forward.DisabledCommandVersion{Start: e.VersionStart, End: e.VersionEnd}}
			dup := false
			for _, x := range next[e.DeviceType] {
				if x == want {
					dup = true
				}
			}
			if !dup {
				next[e.DeviceType] = append(next[e.DeviceType], want)
				changes = append(changes, fmt.Sprintf("disable %s: %s (%s to %s)", e.DeviceType, e.Command, e.VersionStart, e.VersionEnd))
			}
		}
		for _, e := range def.Enable {
			kept := next[e.DeviceType][:0:0]
			removed := 0
			for _, x := range next[e.DeviceType] {
				if x.Command == strings.TrimSpace(e.Command) {
					removed++
					continue
				}
				kept = append(kept, x)
			}
			if removed == 0 {
				return nil, nil, fmt.Errorf("%w: no disabled command %q for %s (inspect-platform area collection_settings lists them)", ErrInvalidInput, e.Command, e.DeviceType)
			}
			if len(kept) == 0 {
				delete(next, e.DeviceType)
			} else {
				next[e.DeviceType] = kept
			}
			changes = append(changes, fmt.Sprintf("enable %s: %s (%d entr%s)", e.DeviceType, e.Command, removed, map[bool]string{true: "y", false: "ies"}[removed == 1]))
		}
		return next, changes, nil
	}
	cur, err := read(ctx)
	if err != nil {
		return nil, err
	}
	next, changes, err := compose(cur)
	if err != nil {
		return nil, err
	}
	types := map[string]bool{}
	for _, e := range def.Disable {
		types[e.DeviceType] = true
	}
	for _, e := range def.Enable {
		types[e.DeviceType] = true
	}
	view := func(m map[string][]forward.DisabledCommand) map[string]any {
		out := map[string]any{}
		for t := range types {
			out[t] = m[t]
		}
		return out
	}
	limits := []string{
		"this is ORGANIZATION-WIDE: it applies to every network and every device of that type, at their next collection. It does not touch snapshots already collected",
		"Forward replaces the whole disabled-commands map on a patch; this sends every other entry back unchanged, read again right before the write. There is no compare-and-swap, so an edit made elsewhere in the same instant could be overwritten",
		"command is a regular expression that must match the WHOLE command text, lowercased (Forward lowercases the pattern too), after any name in it is filled in: for example request system external-list show .* ; an invalid pattern is skipped by Forward without an error. Versions are dotted numbers compared with zero padding and the end is inclusive: an end of 17.99 does not cover 17.99.1; a device whose version cannot be read has nothing disabled",
		"a device that needs a disabled command as a REQUIRED step shows the collection error COMMAND_DISABLED (\"Required command is disabled\") and, per Forward staff, is left out of path search; check investigate-collection-failure view devices after the next collection. An optional command is skipped without an error",
		"this setting also stops a matching command from a network's custom command group (a CLI group of the network, not a Forward built-in), but only in an interactive CLI session on a device whose software version the collector could read; non-interactive SSH agents, endpoint profiles and SNMP groups ignore it. The command is skipped silently (its Command: line is written with nothing under it and the snapshot shows no error; the collector only logs one). A pattern meant for a built-in can therefore switch off a custom one: check inspect-platform area custom_command_groups for a group command it matches before applying. Read from Forward's source by a peer session, not exercised on a live Forward",
		"disabling an external-list command on PAN-OS does not stop the collector downloading each list's URL itself (that fallback is a separate step), so it may save little time",
		"Forward accepts this setting from 26.8.3 (its release notes; Forward staff gave 26.8.3-02 as the first build with it). An older build most likely refuses the property (a 400 naming an unrecognized property) and nothing changes. The device_type must be a connection type name Forward knows (panos_ssh, cisco_ios_xe_ssh, f5_ssh, ...), spelled exactly: an unknown key is rejected",
	}
	if len(goRegexWarn) > 0 {
		limits = append(limits, "Forward lowercases the command text it matches, and the pattern too (read from the collector source), so write the pattern in lower case; these contain upper case: "+strings.Join(goRegexWarn, ", "))
		for _, c := range goRegexWarn {
			if upperClass.MatchString(c) {
				limits = append(limits, fmt.Sprintf("%q uses a class such as \\S or \\D: lowercased it becomes \\s or \\d, the opposite class, so it would match something else than written", c))
			}
		}
	}
	if len(changes) == 0 {
		return &networkPlan{target: "disabled commands", before: view(cur), noop: "the disabled commands already are as asked", limits: limits}, nil
	}
	undo := "run set again with enable_commands for what was disabled, or disable_commands for what was enabled (the before values show the entries)"
	sort.Strings(changes)
	return &networkPlan{target: "change the organization's disabled collection commands: " + strings.Join(changes, "; "), action: "update_disabled_commands", before: view(cur), after: view(next), reversible: true, undo: undo, limits: limits,
		do: func(ctx context.Context) error {
			fresh, err := read(ctx)
			if err != nil {
				return err
			}
			merged, _, err := compose(fresh)
			if err != nil {
				return err
			}
			_, err = s.Client.Collectors.PatchOrganizationSettings(ctx, forward.OrgCollectionSettingsPatch{DisabledCommands: &merged})
			if err != nil && strings.Contains(strings.ToLower(err.Error()), "unrecogni") {
				return fmt.Errorf("this Forward does not accept disabledCommands (it arrived in 26.8.3); nothing changed: %w", err)
			}
			return err
		},
		verify: func(ctx context.Context) (bool, any, error) {
			got, err := read(ctx)
			if err != nil {
				return false, nil, err
			}
			ok := true
			for _, e := range def.Disable {
				found := false
				for _, x := range got[e.DeviceType] {
					if x.Command == e.Command && x.Version.Start == e.VersionStart && x.Version.End == e.VersionEnd {
						found = true
					}
				}
				ok = ok && found
			}
			for _, e := range def.Enable {
				for _, x := range got[e.DeviceType] {
					if x.Command == strings.TrimSpace(e.Command) {
						ok = false
					}
				}
			}
			return ok, view(got), nil
		}}, nil
}
