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
			ManagedDevices []forward.ManagedDevice `json:"managedDevices"`
			CollectorID    string                  `json:"collector_id"`
			ClearCollector bool                    `json:"clear_collector"`
		}
		if err := decodeDefinition(in.Definition, &def, "managedDevices (REPLACES the managed device list), collector_id (move the whole setup to that collector), clear_collector (unpin it: the network's default collector applies)"); err != nil {
			return nil, err
		}
		if def.ManagedDevices == nil && def.CollectorID == "" && !def.ClearCollector {
			return nil, fmt.Errorf("%w: update sets managedDevices (the whole list), collector_id or clear_collector", ErrInvalidInput)
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
		if def.CollectorID != "" {
			what = append(what, "its collector to "+def.CollectorID)
		}
		if def.ClearCollector {
			what = append(what, "its collector (back to the network's default)")
		}
		before, _ := fwd.Generic(c)
		return &networkPlan{target: "change " + strings.Join(what, " and ") + " of controller setup " + c.Name, action: "update_controller_setup", before: before, after: patch, reversible: true, undo: "update again with the before values",
			limits: []string{"managedDevices REPLACES the list: restate every device you keep", "a setup has ONE collector for all its controllers and guests; Forward checks only that the collector belongs to the organization, not that it is assigned to this network, and a collector that does not exist answers 404 that reads like a missing setup"},
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
