package skills

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// planControllerSetup manages a controller-managed setup: controllers (devices Forward logs in to) and the managed devices they report. The controllers' logins are existing credential
// ids, so no secret is read. Deleting one removes the setup and the collection of the devices it manages.
func planControllerSetup(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.ControllerManagedSetups.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.ControllerManagedSetup {
		for i := range cur {
			if strings.EqualFold(cur[i].Name, name) {
				return &cur[i]
			}
		}
		return nil
	}
	present := func(name string, want bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.ControllerManagedSetups.List(ctx, in.NetworkID)
			for _, x := range l {
				if strings.EqualFold(x.Name, name) {
					return want, nil, e
				}
			}
			return !want, nil, e
		}
	}
	switch in.Action {
	case "create":
		var def forward.NewControllerManagedSetup
		if err := decodeDefinition(in.Definition, &def, "name, controllers[{name, type, host, port, cliCredentialId, cliCredential2Id, cliCredential3Id, httpCredentialId (the LOGIN credential of an API-collected controller such as a Versa Director or APIC), snmpCredentialId, jumpServerId}], managedDevices[{name, type, host, cliCredentialId, snmpCredentialId, jumpServerId, collect}]"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" || len(def.Controllers) == 0 {
			return nil, fmt.Errorf("%w: create needs name and at least one controller", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a controller-managed setup named %q already exists", ErrInvalidInput, def.Name)
		}
		return &networkPlan{target: fmt.Sprintf("create controller-managed setup %q on network %s", def.Name, in.NetworkID), action: "create_controller_setup", after: def, reversible: true, undo: "delete the setup (action delete)",
			limits: []string{"this does not test that the controllers accept the credentials: run a collection and read inspect-collection"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ControllerManagedSetups.Create(ctx, in.NetworkID, def)
				return err
			},
			verify: present(def.Name, true)}, nil
	case "test":
		c := find(in.Name)
		if c == nil {
			return nil, nil
		}
		n := len(c.Controllers) + len(c.ManagedDevices)
		var tdef struct {
			WaitSeconds int `json:"wait_seconds"`
		}
		if len(in.Definition) > 0 {
			if err := decodeDefinition(in.Definition, &tdef, "wait_seconds (wait up to this many seconds, at most 600, for every device to have a result newer than the queue time)"); err != nil {
				return nil, err
			}
		}
		var queuedAt time.Time
		var waitFn func(context.Context) (bool, any, error)
		if tdef.WaitSeconds > 0 {
			waitFn = func(ctx context.Context) (bool, any, error) {
				return waitForSetupTests(ctx, s, in.NetworkID, c.Name, &queuedAt, time.Duration(min(tdef.WaitSeconds, 600))*time.Second)
			}
		}
		return &networkPlan{target: fmt.Sprintf("queue a connectivity test of the %d controller(s) and managed device(s) of setup %s", n, c.Name), action: "test_controller_setup",
			after: map[string]any{"setup": c.Name, "devices_tested": n}, reversible: true, undo: "none needed: a test changes no configuration; it replaces each device's last saved test result",
			limits: []string{"Forward answers as soon as the tests are queued, not when they finish: read each device's result with inspect-collection (view config, controller_setups) and compare its tested_at with when you ran this, because the previous result stays until the new one is saved",
				"the route is published by Forward as experimental", "with wait_seconds the apply waits for results newer than the queue time and returns them; without it, read them later with inspect-collection"},
			do: func(ctx context.Context) error {
				queuedAt = time.Now()
				_, err := s.Client.ControllerManagedSetups.Test(ctx, in.NetworkID, "", forward.ControllerAndManagedDeviceSelection{SetupNames: []string{c.Name}})
				return err
			},
			verify: waitFn,
			learn: func(got any, ch *result.Change) {
				if rows, ok := got.([]map[string]any); ok {
					ch.After = map[string]any{"setup": c.Name, "devices_tested": n, "results": rows}
				}
			}}, nil
	case "migrate":
		var def forward.NewControllerManagedSetup
		if err := decodeDefinition(in.Definition, &def, "name, controllers[{name, type, host, port, cliCredentialId, cliCredential2Id, cliCredential3Id, httpCredentialId (the LOGIN credential of an API-collected controller such as a Versa Director or APIC), snmpCredentialId, jumpServerId}], managedDevices[{name, type, host, cliCredentialId, snmpCredentialId, jumpServerId, collect}] (list only the devices to move: a subset is a pilot)"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" || len(def.Controllers) == 0 {
			return nil, fmt.Errorf("%w: migrate needs definition.name (the new setup's name, inside definition, not the top-level name) and at least one controller in definition.controllers", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a controller-managed setup named %q already exists; migrate creates the setup", ErrInvalidInput, def.Name)
		}
		var noLogin []string
		for _, m := range def.ManagedDevices {
			if m.CLICredentialID == "" {
				noLogin = append(noLogin, m.Name)
			}
		}
		if len(noLogin) > 0 {
			return nil, fmt.Errorf("%w: managed device(s) %s have no cliCredentialId: a migrated device inherits nothing from the controller or from the classic device it replaces, so without its own login it cannot be collected", ErrInvalidInput, strings.Join(noLogin, ", "))
		}
		named := map[string]bool{}
		for _, c := range def.Controllers {
			named[strings.ToLower(c.Name)] = true
		}
		for _, m := range def.ManagedDevices {
			named[strings.ToLower(m.Name)] = true
		}
		classic, _, cerr := s.Client.ClassicDevices.List(ctx, in.NetworkID)
		if cerr != nil {
			return nil, cerr
		}
		var gone []map[string]any
		for _, d := range classic {
			if named[strings.ToLower(d.Name)] {
				gone = append(gone, map[string]any{"name": d.Name, "type": d.Type, "host": d.Host, "collector": string(d.CollectorID), "cli_credential": d.CLICredentialID, "cli_credential_2": d.CLICredential2ID, "snmp_credential": d.SNMPCredentialID, "jump_server": d.JumpServerID})
			}
		}
		shown, omitted := result.Cap(gone, 200, "classic devices to delete", "the rest are deleted too; the full list is in the network's classic devices")
		var lim []string
		var pins []string
		for _, g := range gone {
			if c := fmt.Sprint(g["collector"]); c != "" && def.CollectorID == "" {
				pins = append(pins, fmt.Sprintf("%s (%s)", g["name"], c))
			}
		}
		if len(pins) > 0 {
			lim = append(lim, "these classic devices are pinned to a collector: "+strings.Join(pins, ", ")+". A migrated setup has ONE collector for its controllers and guests (collectorId in the definition) and each device's own pin is dropped; with none given the network's default collector is used")
		}
		for _, o := range omitted {
			lim = append(lim, o.Text())
		}
		var off []string
		for _, m := range def.ManagedDevices {
			if m.Collect != nil && !*m.Collect {
				off = append(off, m.Name)
			}
		}
		if len(off) > 0 {
			lim = append(lim, fmt.Sprintf("%d managed device(s) are sent with collect:false (%s). A field report says a migrate produced a setup that read collect true for every device, so Forward may not keep the flag (unverified: the request does carry it). After applying, read inspect-collection view config for these devices; any that read true will be collected at the next collection until restored with controller_setup update (managedDevices restated, collect false)", len(off), strings.Join(off, ", ")))
		}
		return &networkPlan{target: fmt.Sprintf("migrate into new controller-managed setup %q on network %s, deleting %d classic device(s)", def.Name, in.NetworkID, len(gone)), action: "migrate_controller_setup",
			before: map[string]any{"classic_devices_to_delete": shown}, after: def, reversible: false, confirm: def.Name,
			undo: "none: this cannot be undone by update. Recreate the classic devices from the before list (edit-source object classic_device action create), and note their device tags and connectivity test results are gone",
			limits: append(lim,
				"Forward deletes, in one transaction with the create, every classic device whose name matches a controller or managed device of this setup (case-insensitive here; Forward may match exactly), with their per-device settings, connectivity test results and device tags; if the create is refused nothing is deleted (read from Forward's source; not exercised on a live Forward)",
				"a migrated device inherits no login from the controller: each managed device carries its own cliCredentialId, which is checked to be present, not to work. Migrate a small pilot (list a few managed devices) and run a connectivity test before moving the rest",
				"the route is published by Forward as experimental"),
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ControllerManagedSetups.CreateMigratingClassic(ctx, in.NetworkID, def)
				return err
			},
			verify: present(def.Name, true)}, nil
	case "update":
		c := find(in.Name)
		if c == nil {
			return nil, nil
		}
		var def struct {
			ManagedDevices []forward.ManagedDevice    `json:"managedDevices"`
			CollectorID    string                     `json:"collector_id"`
			ClearCollector bool                       `json:"clear_collector"`
			Controllers    []forward.ControllerDevice `json:"controllers"`
			Controller     string                     `json:"controller"`
			ManagedDevice  string                     `json:"managed_device"`
			Set            forward.ClassicDevicePatch `json:"set"`
			// AddManagedDevice and AddController append ONE member: the skill reads the live list, appends it and writes the whole list, so nothing is restated by hand.
			AddManagedDevice *forward.ManagedDevice    `json:"add_managed_device"`
			AddController    *forward.ControllerDevice `json:"add_controller"`
			// ReplaceClassic deletes the classic device that has the added member's name first (Forward refuses a member whose name a classic device uses).
			ReplaceClassic bool `json:"replace_classic"`
			// RemoveManagedDevice and RemoveController delete ONE member by name with Forward's single-member route; the other members are not touched.
			RemoveManagedDevice string `json:"remove_managed_device"`
			RemoveController    string `json:"remove_controller"`
		}
		if err := decodeDefinition(in.Definition, &def, "add_managed_device or add_controller (ONE member to append; replace_classic deletes the classic device of the same name first), remove_managed_device or remove_controller (a member name), or controller or managed_device (the name of ONE member) with set {name, host, type, port, cliCredentialId, cliCredential2Id, cliCredential3Id, httpCredentialId, collect, note, ...} changes just that member in place (a rename keeps its test result); or controllers (REPLACES the whole controller list: read the live list first and restate it, Forward refuses an empty one) or managedDevices (REPLACES the managed device list), collector_id (move the whole setup to that collector), clear_collector (unpin it: the network's default collector applies)"); err != nil {
			return nil, err
		}
		if def.ReplaceClassic && def.AddManagedDevice == nil && def.AddController == nil {
			return nil, fmt.Errorf("%w: replace_classic belongs to add_managed_device or add_controller", ErrInvalidInput)
		}
		if (def.AddManagedDevice != nil || def.AddController != nil || def.RemoveManagedDevice != "" || def.RemoveController != "") && (def.Controller != "" || def.ManagedDevice != "") {
			return nil, fmt.Errorf("%w: an add or remove cannot be combined with a member change (controller or managed_device with set): run them separately", ErrInvalidInput)
		}
		if def.RemoveManagedDevice != "" || def.RemoveController != "" {
			return planControllerMemberRemove(ctx, s, in, c, def.RemoveManagedDevice, def.RemoveController, def.AddManagedDevice != nil || def.AddController != nil || def.ManagedDevices != nil || def.Controllers != nil || def.CollectorID != "" || def.ClearCollector || def.Controller != "" || def.ManagedDevice != "" || def.ReplaceClassic)
		}
		if def.AddManagedDevice != nil || def.AddController != nil {
			return planControllerMemberAdd(ctx, s, in, c, def.AddManagedDevice, def.AddController, def.ReplaceClassic, def.ManagedDevices != nil || def.Controllers != nil || def.CollectorID != "" || def.ClearCollector || def.Controller != "" || def.ManagedDevice != "")
		}
		if def.Controller != "" || def.ManagedDevice != "" {
			return planControllerMemberPatch(ctx, s, in, c, def.Controller, def.ManagedDevice, def.Set, def.ManagedDevices != nil || def.Controllers != nil || def.CollectorID != "" || def.ClearCollector)
		}
		if def.ManagedDevices == nil && def.Controllers == nil && def.CollectorID == "" && !def.ClearCollector {
			return nil, fmt.Errorf("%w: update sets controllers or managedDevices (the whole list), collector_id or clear_collector, or changes one member (controller or managed_device with set)", ErrInvalidInput)
		}
		if def.CollectorID != "" && def.ClearCollector {
			return nil, fmt.Errorf("%w: collector_id and clear_collector cannot both be given", ErrInvalidInput)
		}
		patch := forward.ControllerManagedSetupPatch{CollectorID: forward.Identifier(def.CollectorID), ClearCollector: def.ClearCollector}
		var what []string
		if def.ManagedDevices != nil {
			patch.ManagedDevices = &def.ManagedDevices
			what = append(what, "the managed devices")
		}
		if def.Controllers != nil {
			if len(def.Controllers) == 0 {
				return nil, fmt.Errorf("%w: controllers cannot be empty: Forward refuses an empty controller list", ErrInvalidInput)
			}
			patch.Controllers = &def.Controllers
			what = append(what, "the whole controller list")
		}
		if def.CollectorID != "" {
			what = append(what, "its collector to "+def.CollectorID)
		}
		if def.ClearCollector {
			what = append(what, "its collector (back to the network's default)")
		}
		before, _ := fwd.Generic(c)
		return &networkPlan{target: "change " + strings.Join(what, " and ") + " of controller setup " + c.Name, action: "update_controller_setup", before: before, after: patch, reversible: true, undo: "update again with the before values",
			limits: []string{"managedDevices and controllers each REPLACE their whole list: read the live list first and restate every entry you keep (a stale list undoes a change made elsewhere, such as a rename); to change ONE member use controller or managed_device with set", "a setup has ONE collector for all its controllers and guests; Forward checks only that the collector belongs to the organization, not that it is assigned to this network, and a collector that does not exist answers 404 that reads like a missing setup"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ControllerManagedSetups.Patch(ctx, in.NetworkID, c.Name, patch)
				return err
			}}, nil
	case "delete":
		c := find(in.Name)
		if c == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(c)
		return &networkPlan{target: "delete controller-managed setup " + c.Name, action: "delete_controller_setup", before: before, reversible: false, confirm: c.Name,
			undo: "none: create it again from the before values", limits: []string{"the devices it manages are no longer collected through it"},
			do: func(ctx context.Context) error {
				_, err := s.Client.ControllerManagedSetups.Delete(ctx, in.NetworkID, c.Name)
				return err
			},
			verify: present(c.Name, false)}, nil
	}
	return nil, fmt.Errorf("%w: controller_setup takes action create, migrate, test, update or delete", ErrInvalidInput)
}

// planMistSetup manages a Juniper Mist cloud-managed setup. apiKeyId names an existing credential, so no secret is read here.
func planMistSetup(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.CloudManagedSetups.ListMist(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.MistSetup {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	present := func(name string, want bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.CloudManagedSetups.ListMist(ctx, in.NetworkID)
			for _, x := range l {
				if x.Name == name {
					return want, nil, e
				}
			}
			return !want, nil, e
		}
	}
	switch in.Action {
	case "create":
		var def forward.NewMistSetup
		if err := decodeDefinition(in.Definition, &def, "name, region (GLOBAL_01..05, EMEA_01..04, APAC_01..03), apiKeyId, collect, collectorId, hosts, concurrency"); err != nil {
			return nil, err
		}
		if def.Name == "" || def.Region == "" || def.APIKeyID == "" {
			return nil, fmt.Errorf("%w: create needs name, region and apiKeyId", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a Mist setup named %q already exists", ErrInvalidInput, def.Name)
		}
		return &networkPlan{target: fmt.Sprintf("create Mist setup %q on network %s", def.Name, in.NetworkID), action: "create_mist_setup", after: def, reversible: true, undo: "delete the setup (action delete)",
			limits: []string{"this does not test the API key: run a collection and read inspect-collection"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.CloudManagedSetups.CreateMist(ctx, in.NetworkID, def)
				return err
			},
			verify: present(def.Name, true)}, nil
	case "delete":
		m := find(in.Name)
		if m == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(m)
		return &networkPlan{target: "delete Mist setup " + m.Name, action: "delete_mist_setup", before: before, reversible: false, confirm: m.Name,
			undo: "none: create it again from the before values",
			do: func(ctx context.Context) error {
				_, err := s.Client.CloudManagedSetups.DeleteMist(ctx, in.NetworkID, m.Name)
				return err
			},
			verify: present(m.Name, false)}, nil
	}
	return nil, fmt.Errorf("%w: mist_setup takes action create or delete", ErrInvalidInput)
}

// setupTestPollEvery is how often waitForSetupTests re-reads the setup.
var setupTestPollEvery = 3 * time.Second

// waitForSetupTests polls a setup's devices until each has a saved test result that ended after the tests were queued (a result keeps the previous test's time until the new one is saved), or the wait runs out.
// It returns whether every device has a fresh result, with each device's outcome.
func waitForSetupTests(ctx context.Context, s *fwd.Session, networkID, setup string, queuedAt *time.Time, wait time.Duration) (bool, any, error) {
	deadline := time.Now().Add(wait)
	since := queuedAt.Add(-5 * time.Second) // a little slack for the difference between Forward's clock and this one
	for {
		setups, _, err := s.Client.ControllerManagedSetups.ListWithTestResults(ctx, networkID)
		if err != nil {
			return false, nil, err
		}
		var rows []map[string]any
		fresh := true
		found := false
		for _, st := range setups {
			if !strings.EqualFold(st.Name, setup) {
				continue
			}
			found = true
			add := func(name, role string, tr *forward.DeviceTestResult) {
				outcome, at := setupTestOutcome(tr)
				t, perr := time.Parse(time.RFC3339, at)
				isFresh := tr != nil && perr == nil && t.After(since)
				if !isFresh {
					fresh = false
				}
				rows = append(rows, map[string]any{"name": name, "role": role, "test": outcome, "tested_at": at, "newer_than_queue_time": isFresh})
			}
			for _, c := range st.Controllers {
				add(c.Name, "controller", c.TestResult)
			}
			for _, m := range st.ManagedDevices {
				add(m.Name, "managed", m.TestResult)
			}
		}
		if found && fresh {
			return true, rows, nil
		}
		if time.Now().After(deadline) {
			return false, rows, nil
		}
		select {
		case <-ctx.Done():
			return false, rows, ctx.Err()
		case <-time.After(setupTestPollEvery):
		}
	}
}

// planControllerMemberPatch changes one controller or one managed device of a setup in place (name, host, collect and the rest of
// ClassicDevicePatch), the route Forward gives for it: the other members, their tags and test results are untouched, and a rename
// keeps the test result.
func planControllerMemberPatch(ctx context.Context, s *fwd.Session, in editSourceInput, c *forward.ControllerManagedSetup, controller, managed string, set forward.ClassicDevicePatch, mixed bool) (*networkPlan, error) {
	if (controller != "") == (managed != "") {
		return nil, fmt.Errorf("%w: give controller or managed_device (one member name), not both", ErrInvalidInput)
	}
	if mixed {
		return nil, fmt.Errorf("%w: a member change (controller or managed_device with set) cannot be combined with managedDevices, collector_id or clear_collector: run them separately", ErrInvalidInput)
	}
	if set.CollectorID != nil {
		return nil, fmt.Errorf("%w: a member has no collector of its own: set collector_id on the setup", ErrInvalidInput)
	}
	kind, name := "controller", controller
	var before any
	if controller != "" {
		for _, d := range c.Controllers {
			if strings.EqualFold(d.Name, controller) {
				name = d.Name
				before, _ = fwd.Generic(d)
			}
		}
	} else {
		kind, name = "managed device", managed
		for _, d := range c.ManagedDevices {
			if strings.EqualFold(d.Name, managed) {
				name = d.Name
				before, _ = fwd.Generic(d)
			}
		}
	}
	if before == nil {
		return nil, fmt.Errorf("%w: setup %q has no %s named %q", ErrInvalidInput, c.Name, kind, name)
	}
	return &networkPlan{target: fmt.Sprintf("change %s %s of controller setup %s in place", kind, name, c.Name), action: "patch_controller_setup_member", before: before, after: set, reversible: true,
		undo: "patch the member again with the before values (a rename: patch the new name back to the old)",
		limits: []string{"only the properties in set change; the other members, per-device tags and test results are untouched, and a rename keeps the test result",
			"a rename is not idempotent: the old name answers 404 afterwards, so a retry after a success fails (read the setup first)",
			"read from Forward's source through the SDK; the route is Preview and was not run against a live Forward by this skill"},
		do: func(ctx context.Context) error {
			if controller != "" {
				_, _, err := s.Client.ControllerManagedSetups.PatchController(ctx, in.NetworkID, c.Name, name, set)
				return err
			}
			_, _, err := s.Client.ControllerManagedSetups.PatchManagedDevice(ctx, in.NetworkID, c.Name, name, set)
			return err
		}}, nil
}

// planControllerMemberAdd appends ONE managed device or controller to a live setup. Forward has no single-member add (POST on a member path is only action=test), so the whole list is written; the skill
// reads the live list itself, so the caller restates nothing, and after the write it reads the setup again and checks that every member that was not touched is identical to what was read and that the
// added member kept what was sent (collect included). A member whose name a classic device uses is refused by Forward at apply ("Device name(s) already used"): the dry run checks that too, and with
// replace_classic it deletes that classic device first (not atomic: if the add then fails the classic device is gone, and its full definition is in the result).
func planControllerMemberAdd(ctx context.Context, s *fwd.Session, in editSourceInput, c *forward.ControllerManagedSetup, md *forward.ManagedDevice, ctl *forward.ControllerDevice, replaceClassic, mixed bool) (*networkPlan, error) {
	if (md != nil) == (ctl != nil) {
		return nil, fmt.Errorf("%w: give add_managed_device or add_controller (one member), not both", ErrInvalidInput)
	}
	if mixed {
		return nil, fmt.Errorf("%w: an add cannot be combined with managedDevices, controllers, collector_id, clear_collector or a member change: run them separately", ErrInvalidInput)
	}
	name, kind := "", "managed device"
	if md != nil {
		name = md.Name
		if md.CLICredentialID == "" && md.HTTPCredentialID == "" {
			return nil, fmt.Errorf("%w: managed device %q has no cliCredentialId (or httpCredentialId): a member inherits nothing from the controller, so without its own login it cannot be collected", ErrInvalidInput, name)
		}
	} else {
		name, kind = ctl.Name, "controller"
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: the added member needs a name", ErrInvalidInput)
	}
	for _, m := range c.ManagedDevices {
		if strings.EqualFold(m.Name, name) {
			return nil, fmt.Errorf("%w: setup %q already has a managed device named %q", ErrInvalidInput, c.Name, name)
		}
	}
	for _, m := range c.Controllers {
		if strings.EqualFold(m.Name, name) {
			return nil, fmt.Errorf("%w: setup %q already has a controller named %q", ErrInvalidInput, c.Name, name)
		}
	}
	classic, _, cerr := s.Client.ClassicDevices.List(ctx, in.NetworkID)
	if cerr != nil {
		return nil, cerr
	}
	// Forward refuses two members with the same host and port ("Duplicate devices: [a, b] have host: x, port: default", seen live), so the dry run checks that too: against the setup's own
	// members and the network's classic devices (the classic device this add replaces is not a duplicate of itself).
	var newHost string
	var newPort *int32
	if md != nil {
		newHost, newPort = md.Host, md.Port
	} else {
		newHost, newPort = ctl.Host, ctl.Port
	}
	samePort := func(a, b *int32) bool {
		pa, pb := int32(0), int32(0)
		if a != nil {
			pa = *a
		}
		if b != nil {
			pb = *b
		}
		return pa == pb
	}
	if h := strings.TrimSpace(newHost); h != "" {
		var dups []string
		for _, m := range c.ManagedDevices {
			if strings.EqualFold(m.Host, h) && samePort(m.Port, newPort) {
				dups = append(dups, m.Name)
			}
		}
		for _, m := range c.Controllers {
			if strings.EqualFold(m.Host, h) && samePort(m.Port, newPort) {
				dups = append(dups, m.Name)
			}
		}
		for _, d := range classic {
			if strings.EqualFold(d.Host, h) && samePort(d.Port, newPort) && !(replaceClassic && strings.EqualFold(d.Name, name)) {
				dups = append(dups, d.Name+" (classic device)")
			}
		}
		if len(dups) > 0 {
			return nil, fmt.Errorf("%w: host %q (same port) is already used by %s: Forward refuses two devices with the same host and port (400 \"Duplicate devices\")", ErrInvalidInput, h, strings.Join(dups, ", "))
		}
	}
	var clash map[string]any
	classicName := ""
	for _, d := range classic {
		if strings.EqualFold(d.Name, name) {
			classicName = d.Name
			clash = map[string]any{"name": d.Name}
			if g, _ := fwd.Generic(d); g != nil {
				if m, ok := g.(map[string]any); ok {
					clash = m
				}
			}
			if len(d.Raw) > 0 {
				clash["raw"] = d.Raw // the complete wire object: what recreating it needs
			}
		}
	}
	if clash != nil && !replaceClassic {
		return nil, fmt.Errorf("%w: a classic device named %q exists, and Forward refuses a member whose name a classic device uses (400 \"Device name(s) already used\") at apply. Delete the classic device first (edit-source classic_device delete) or set replace_classic to delete it as part of this add", ErrInvalidInput, name)
	}
	if clash == nil && replaceClassic {
		return nil, fmt.Errorf("%w: replace_classic is set but no classic device is named %q", ErrInvalidInput, name)
	}
	// the new whole list
	var patch forward.ControllerManagedSetupPatch
	before, _ := fwd.Generic(map[string]any{"managed_devices": len(c.ManagedDevices), "controllers": len(c.Controllers)})
	var added any
	if md != nil {
		all := append(append([]forward.ManagedDevice{}, c.ManagedDevices...), *md)
		patch.ManagedDevices = &all
		added, _ = fwd.Generic(*md)
	} else {
		all := append(append([]forward.ControllerDevice{}, c.Controllers...), *ctl)
		patch.Controllers = &all
		added, _ = fwd.Generic(*ctl)
	}
	untouched := len(c.ManagedDevices) + len(c.Controllers)
	setupName := c.Name
	snapshotBefore := map[string]any{}
	for _, m := range c.ManagedDevices {
		g, _ := fwd.Generic(m)
		snapshotBefore["m:"+strings.ToLower(m.Name)] = g
	}
	for _, m := range c.Controllers {
		g, _ := fwd.Generic(m)
		snapshotBefore["c:"+strings.ToLower(m.Name)] = g
	}
	limits := []string{
		fmt.Sprintf("Forward has no single-member add, so the whole list is written; this read the live list (%d members) and appended one, and after the write it reads the setup again and checks that each of the %d other members is identical to what was read, in the fields this SDK models", untouched, untouched),
		"the SDK documents that a list read back and re-sent keeps every property it models EXCEPT a managed device's hidden concurrency setting and its attribution (who and when): a whole-list write may reset those on every member, and this check cannot see them (changing one member in place with managed_device and set does not have this effect)",
		"between the read and the write another change could be overwritten: the apply reads the setup again first and stops, writing nothing, if any member differs from the plan's read; a change in the instant between that check and the write is not caught",
		"name matching: a classic device is matched to the member name ignoring case here; whether Forward compares names exactly or ignoring case is not verified (the migrate path says the same), and a name used by another setup's member or another source type is not checked",
		"a duplicate name among this setup's members and the network's classic devices was checked; a duplicate name among the network's other source types was not (Forward refuses it at apply); the host and port were checked against this setup's members and the network's classic devices, not against other setups",
		"read from Forward's source and a field report, not run by this skill against a live Forward",
	}
	action := "add_controller_setup_member"
	target := fmt.Sprintf("add %s %q to controller setup %s (the other %d members are written back as read)", kind, name, setupName, untouched)
	confirm := ""
	undo := "remove the member again (update with remove_" + strings.ReplaceAll(kind, " ", "_") + " and confirm = its name)"
	if clash != nil {
		confirm = name
		target += fmt.Sprintf(" AFTER deleting the classic device %q", name)
		undo = "none for the classic device: its definition is in the before state; recreate it with edit-source classic_device create. The added member can be removed again"
		limits = append(limits, "replace_classic is NOT atomic: the classic device is deleted first, then the member is added; if the add fails the classic device stays deleted (its full definition is returned) and the device is not collected until it is recreated or added again; its per-device tags, test results and settings that classic_device create does not take are lost")
		before, _ = fwd.Generic(map[string]any{"managed_devices": len(c.ManagedDevices), "controllers": len(c.Controllers), "classic_device_to_delete": clash})
	}
	return &networkPlan{target: target, action: action, before: before, after: added, reversible: clash == nil, confirm: confirm, undo: undo, limits: limits,
		do: func(ctx context.Context) error {
			// the plan was built from a read a moment ago: read the setup again and stop if anyone changed it, so a concurrent change is not overwritten by the whole-list write
			if l, _, rerr := s.Client.ControllerManagedSetups.List(ctx, in.NetworkID); rerr == nil {
				for _, x := range l {
					if !strings.EqualFold(x.Name, setupName) {
						continue
					}
					now := map[string]any{}
					for _, m := range x.ManagedDevices {
						g, _ := fwd.Generic(m)
						now["m:"+strings.ToLower(m.Name)] = g
					}
					for _, m := range x.Controllers {
						g, _ := fwd.Generic(m)
						now["c:"+strings.ToLower(m.Name)] = g
					}
					if !reflect.DeepEqual(now, snapshotBefore) {
						return fmt.Errorf("setup %q changed since it was read (another change added, removed or edited a member): nothing was written; run the dry run again", setupName)
					}
				}
			}
			if clash != nil {
				if _, err := s.Client.ClassicDevices.Delete(ctx, in.NetworkID, classicName); err != nil {
					return fmt.Errorf("deleting the classic device %q failed, nothing was added: %w", classicName, err)
				}
			}
			if _, _, err := s.Client.ControllerManagedSetups.Patch(ctx, in.NetworkID, setupName, patch); err != nil {
				if clash != nil {
					return &partialApplyError{msg: fmt.Sprintf("the classic device %q WAS deleted but adding the member failed: %v. Recreate the classic device from detail.classic_device_deleted (edit-source classic_device create), or run the add again after fixing the cause", classicName, err),
						detail: map[string]any{"classic_device_deleted": clash, "add_error": err.Error()}}
				}
				return err
			}
			return nil
		},
		verify: func(ctx context.Context) (bool, any, error) {
			l, _, err := s.Client.ControllerManagedSetups.List(ctx, in.NetworkID)
			if err != nil {
				return false, nil, err
			}
			for _, x := range l {
				if !strings.EqualFold(x.Name, setupName) {
					continue
				}
				now := map[string]any{}
				for _, m := range x.ManagedDevices {
					g, _ := fwd.Generic(m)
					now["m:"+strings.ToLower(m.Name)] = g
				}
				for _, m := range x.Controllers {
					g, _ := fwd.Generic(m)
					now["c:"+strings.ToLower(m.Name)] = g
				}
				var changed []string
				for k, v := range snapshotBefore {
					if !reflect.DeepEqual(v, now[k]) {
						changed = append(changed, k)
					}
				}
				sort.Strings(changed)
				key := "m:" + strings.ToLower(name)
				if md == nil {
					key = "c:" + strings.ToLower(name)
				}
				got, found := now[key]
				if !found {
					return false, map[string]any{"added_member_present": false}, nil
				}
				var sentCollect *bool
				if md != nil {
					sentCollect = md.Collect
				} else {
					sentCollect = ctl.Collect
				}
				if sentCollect != nil {
					gm, _ := got.(map[string]any)
					read := true // Forward treats an absent collect as true
					if v, ok := gm["collect"].(bool); ok {
						read = v
					}
					if read != *sentCollect {
						return false, map[string]any{"added_member_collect_sent": *sentCollect, "added_member_collect_read_back": gm["collect"]}, nil
					}
				}
				if len(changed) > 0 {
					return false, map[string]any{"members_that_changed_in_the_write": changed}, nil
				}
				return true, nil, nil
			}
			return false, map[string]any{"setup_present": false}, nil
		}}, nil
}

// planControllerMemberRemove deletes ONE managed device or controller by name (Forward's single-member DELETE). The other members, their tests and tags are not touched. Forward refuses to remove the
// last controller (400, read from its source) and the SDK treats a missing member as success, so the dry run checks the member exists.
func planControllerMemberRemove(ctx context.Context, s *fwd.Session, in editSourceInput, c *forward.ControllerManagedSetup, managed, controller string, mixed bool) (*networkPlan, error) {
	if (managed != "") == (controller != "") {
		return nil, fmt.Errorf("%w: give remove_managed_device or remove_controller (one member name), not both", ErrInvalidInput)
	}
	if mixed {
		return nil, fmt.Errorf("%w: a remove cannot be combined with an add, a whole-list update, collector_id or a member change: run them separately", ErrInvalidInput)
	}
	kind, name := "managed device", managed
	var before any
	if managed != "" {
		for _, d := range c.ManagedDevices {
			if strings.EqualFold(d.Name, managed) {
				name = d.Name
				before, _ = fwd.Generic(d)
			}
		}
	} else {
		kind, name = "controller", controller
		for _, d := range c.Controllers {
			if strings.EqualFold(d.Name, controller) {
				name = d.Name
				before, _ = fwd.Generic(d)
			}
		}
		if before != nil && len(c.Controllers) == 1 {
			return nil, fmt.Errorf("%w: %q is the only controller of setup %q and Forward refuses to leave a setup with no controller (delete the setup instead)", ErrInvalidInput, name, c.Name)
		}
	}
	if before == nil {
		return nil, fmt.Errorf("%w: setup %q has no %s named %q", ErrInvalidInput, c.Name, kind, name)
	}
	if managed != "" && len(c.ManagedDevices) == 1 {
		return nil, fmt.Errorf("%w: %q is the only managed device of setup %q and the SDK documents that removing the last managed device is a 400 (delete the setup, or add another member first)", ErrInvalidInput, name, c.Name)
	}
	setupName := c.Name
	untouched := len(c.ManagedDevices) + len(c.Controllers) - 1
	others := map[string]any{}
	for _, m := range c.ManagedDevices {
		if managed != "" && strings.EqualFold(m.Name, name) {
			continue
		}
		g, _ := fwd.Generic(m)
		others["m:"+strings.ToLower(m.Name)] = g
	}
	for _, m := range c.Controllers {
		if controller != "" && strings.EqualFold(m.Name, name) {
			continue
		}
		g, _ := fwd.Generic(m)
		others["c:"+strings.ToLower(m.Name)] = g
	}
	return &networkPlan{target: fmt.Sprintf("remove %s %q from controller setup %s (the other %d members are not touched)", kind, name, setupName, untouched), action: "remove_controller_setup_member", before: before, reversible: false, confirm: name,
		undo: "add it back with add_managed_device or add_controller using the before values; its last connectivity test result and per-device tags are not restored",
		limits: []string{"uses Forward's single-member DELETE (SDK v0.8.56), read from Forward's source and not run by this skill against a live Forward; a member that does not exist is a success there, so the dry run checks that it exists",
			"the device is no longer collected through the setup; if it is a managed device its data leaves the next snapshot",
			"after the delete the setup is read again and each of the other members must be identical to what was read"},
		do: func(ctx context.Context) error {
			if managed != "" {
				_, err := s.Client.ControllerManagedSetups.DeleteManagedDevice(ctx, in.NetworkID, setupName, name)
				return err
			}
			_, err := s.Client.ControllerManagedSetups.DeleteController(ctx, in.NetworkID, setupName, name)
			return err
		},
		verify: func(ctx context.Context) (bool, any, error) {
			l, _, err := s.Client.ControllerManagedSetups.List(ctx, in.NetworkID)
			if err != nil {
				return false, nil, err
			}
			for _, x := range l {
				if !strings.EqualFold(x.Name, setupName) {
					continue
				}
				now := map[string]any{}
				for _, m := range x.ManagedDevices {
					g, _ := fwd.Generic(m)
					now["m:"+strings.ToLower(m.Name)] = g
				}
				for _, m := range x.Controllers {
					g, _ := fwd.Generic(m)
					now["c:"+strings.ToLower(m.Name)] = g
				}
				key := "m:" + strings.ToLower(name)
				if controller != "" {
					key = "c:" + strings.ToLower(name)
				}
				if _, still := now[key]; still {
					return false, map[string]any{"member_still_present": true}, nil
				}
				var changed []string
				for k, v := range others {
					if !reflect.DeepEqual(v, now[k]) {
						changed = append(changed, k)
					}
				}
				sort.Strings(changed)
				if len(changed) > 0 {
					return false, map[string]any{"members_that_changed": changed}, nil
				}
				return true, nil, nil
			}
			return false, map[string]any{"setup_present": false}, nil
		}}, nil
}
