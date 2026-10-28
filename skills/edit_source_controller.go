package skills

import (
	"context"
	"fmt"
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
			return nil, fmt.Errorf("%w: migrate needs name and at least one controller", ErrInvalidInput)
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
		}
		if err := decodeDefinition(in.Definition, &def, "controller or managed_device (the name of ONE member) with set {name, host, type, port, cliCredentialId, cliCredential2Id, cliCredential3Id, httpCredentialId, collect, note, ...} changes just that member in place (a rename keeps its test result); or controllers (REPLACES the whole controller list: read the live list first and restate it, Forward refuses an empty one) or managedDevices (REPLACES the managed device list), collector_id (move the whole setup to that collector), clear_collector (unpin it: the network's default collector applies)"); err != nil {
			return nil, err
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
