package skills

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

func planClassicDevice(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.ClassicDevices.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.ClassicDevice {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	if in.Action == "test" {
		return planClassicDeviceTest(ctx, s, in, cur)
	}
	fields := "name, host, type, port, cliCredentialId, cliCredential2Id (the PRIVILEGED_MODE / enable credential), cliCredential3Id (EXPERT_MODE), httpCredentialId, snmpCredentialId, jumpServerId, collectorId, collect, note, enableSnmpCollection, collectBgpAdvertisements, bgpTableType, bgpPeerType, bgpAndBmpFailureHandling, disableIpv6Collection, fullCollectionLog"
	switch in.Action {
	case "delete":
		if in.Name == "" || len(in.Definition) > 0 {
			return nil, fmt.Errorf("%w: delete takes name (the device) and confirm only", ErrInvalidInput)
		}
		d := find(in.Name)
		if d == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(d)
		return &networkPlan{target: fmt.Sprintf("delete device %q from the collection list of network %s", in.Name, in.NetworkID), action: "delete_device", before: before, after: nil, reversible: true, confirm: in.Name,
			undo:   "add it again with the before values (edit-source object classic_device action create)",
			limits: []string{"removing a device from the list stops it being collected; its past snapshots keep it. This changes the list of sources, not the device"},
			do: func(ctx context.Context) error {
				_, err := s.Client.ClassicDevices.Delete(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.ClassicDevices.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == in.Name {
						return false, map[string]any{"still_listed": true}, e
					}
				}
				return true, nil, e
			}}, nil
	case "create":
		var def classicDeviceDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if def.Name == "" || def.Host == "" {
			return nil, fmt.Errorf("%w: create needs name and host", ErrInvalidInput)
		}
		if err := checkDeviceOptions(def.BGPTable, def.BGPPeer, def.BGPFailure); err != nil {
			return nil, err
		}
		if err := bgpNeedsAdvertisements(def.BGPTable, def.BGPPeer, def.CollectBGP); err != nil {
			return nil, err
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a device named %q is already in the list", ErrInvalidInput, def.Name)
		}
		req := forward.ClassicDeviceRequest{Name: def.Name, Host: def.Host, Type: def.Type, Port: def.Port, CLICredentialID: def.CLICredentialID, CLICredential2ID: def.CLICredential2ID, CLICredential3ID: def.CLICredential3ID,
			HTTPCredentialID: def.HTTPCredentialID, SNMPCredentialID: def.SNMPCredentialID, JumpServerID: def.JumpServerID, CollectorID: def.CollectorID, Collect: def.Collect, Note: def.Note,
			EnableSNMPCollection: def.EnableSNMP, CollectBGPAdvertisements: def.CollectBGP, BGPTableType: def.BGPTable, BGPPeerType: def.BGPPeer, BGPAndBMPFailureHandling: def.BGPFailure, DisableIPv6Collection: def.NoIPv6, FullCollectionLog: def.FullLog}
		return &networkPlan{target: fmt.Sprintf("add device %q (%s) to the collection list of network %s", def.Name, def.Host, in.NetworkID), action: "add_device", before: nil, after: def, reversible: true,
			undo:   "delete it from the list (edit-source object classic_device action delete)",
			limits: []string{"credentials and the jump server are referred to by id (inspect-platform areas credentials, jump_servers); it is collected from the next collection, which edit-collection can start"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ClassicDevices.Create(ctx, in.NetworkID, req)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.ClassicDevices.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == def.Name {
						return true, nil, e
					}
				}
				return false, nil, e
			}}, nil
	case "update":
		if hasBulkDevices(in.Definition) {
			return planDevicesBulk(ctx, s, in, cur)
		}
		var def classicDeviceDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		d := find(in.Name)
		if d == nil {
			return nil, nil
		}
		patch, err := def.patchFor(d)
		if err != nil {
			return nil, err
		}
		rename := def.Name != "" && def.Name != in.Name
		if rename {
			if err := renameCheck(ctx, s, in.NetworkID, in.Name, def.Name, cur); err != nil {
				return nil, err
			}
			nn := def.Name
			patch.Name = &nn
		}
		before, _ := fwd.Generic(d)
		after, _ := fwd.Generic(patch) // only the fields that change: the update is partial, so the rest of the device stays as it is
		if am, ok := after.(map[string]any); ok && len(patch.Clear) > 0 {
			am["clear"] = patch.Clear
		}
		limits := []string{"after lists only the fields this update changes; every other field of the device stays as it is"}
		target, undo := fmt.Sprintf("update device %q on network %s", in.Name, in.NetworkID), "update it again with the before values"
		if rename {
			target = fmt.Sprintf("rename device %q to %q on network %s", in.Name, def.Name, in.NetworkID)
			undo = fmt.Sprintf("rename it back (edit-source classic_device update name %q with definition.name %q)", def.Name, in.Name)
			limits = append(limits, "a rename applies on the next collection (expected behaviour, not verified): collected snapshots keep the old name; tags, layout, link overrides and synthetic-device references follow the rename; the old name answers 404 afterwards and a rename is not idempotent, so do not apply it twice", "the new name must be unique ignoring case across ALL collection source names; this checked classic devices and controller setups, and Forward also refuses a name used by another source type or a synthetic device (400)")
		}
		return &networkPlan{target: target, action: "update_device", before: before, after: after, reversible: true, undo: undo,
			limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ClassicDevices.Patch(ctx, in.NetworkID, in.Name, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				want := in.Name
				if rename {
					want = def.Name
				}
				l, _, e := s.Client.ClassicDevices.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == want {
						ok, bad := deviceHolds(x, patch)
						if !ok {
							return false, map[string]any{"fields_not_as_asked": bad}, e
						}
						return true, nil, e
					}
				}
				return false, map[string]any{"device_listed": false}, e
			}}, nil
	}
	return nil, fmt.Errorf("%w: object classic_device takes action create, update or delete", ErrInvalidInput)
}

// checkDeviceOptions checks a device's BGP collection options against Forward's enums (NewClassicDevice), so a typo fails at the dry run, not as a 400.
func checkDeviceOptions(table, peer, failure string) error {
	in := func(field, v string, allowed ...string) error {
		if v == "" {
			return nil
		}
		for _, a := range allowed {
			if v == a {
				return nil
			}
		}
		return fmt.Errorf("%w: %s %q is not one of %s", ErrInvalidInput, field, v, strings.Join(allowed, ", "))
	}
	if err := in("bgpTableType", table, "BOTH", "ADJ_RIB_IN", "ADJ_RIB_OUT"); err != nil {
		return err
	}
	if err := in("bgpPeerType", peer, "BOTH", "EBGP", "IBGP"); err != nil {
		return err
	}
	return in("bgpAndBmpFailureHandling", failure, "FAIL_DEVICE", "SWITCH_TO_CLI", "ALWAYS_USE_CLI", "ALWAYS_USE_BMP", "CONTINUE_WITHOUT_BGP_DATA")
}

// bgpNeedsAdvertisements refuses a BGP table or peer type while advertisement collection is off: Forward honours them only when collectBgpAdvertisements is true and drops them otherwise.
func bgpNeedsAdvertisements(table, peer string, collect *bool) error {
	if (table != "" || peer != "") && (collect == nil || !*collect) {
		return fmt.Errorf("%w: bgpTableType and bgpPeerType take effect only when collectBgpAdvertisements is true (Forward drops them otherwise); set it in the same call", ErrInvalidInput)
	}
	return nil
}

// classicDeviceDef is the definition of a classic device: the fields create takes and update may change. A zero value is "not given".
type classicDeviceDef struct {
	Name             string `json:"name"`
	Host             string `json:"host"`
	Type             string `json:"type"`
	Port             *int32 `json:"port"`
	CLICredentialID  string `json:"cliCredentialId"`
	CLICredential2ID string `json:"cliCredential2Id"`
	CLICredential3ID string `json:"cliCredential3Id"`
	HTTPCredentialID string `json:"httpCredentialId"`
	SNMPCredentialID string `json:"snmpCredentialId"`
	JumpServerID     string `json:"jumpServerId"`
	CollectorID      string `json:"collectorId"`
	Collect          *bool  `json:"collect"`
	Note             string `json:"note"`
	// collection options
	EnableSNMP *bool  `json:"enableSnmpCollection"`
	CollectBGP *bool  `json:"collectBgpAdvertisements"`
	BGPTable   string `json:"bgpTableType"`
	BGPPeer    string `json:"bgpPeerType"`
	BGPFailure string `json:"bgpAndBmpFailureHandling"`
	NoIPv6     *bool  `json:"disableIpv6Collection"`
	FullLog    *bool  `json:"fullCollectionLog"`
	// session options (SSH and Telnet), BMP and BGP subnets, and SNMP performance thresholds (fractions): update only
	PaginationMode       string   `json:"paginationMode"`
	TerminalWidth        *int32   `json:"terminalWidth"`
	LargeRTT             *bool    `json:"largeRtt"`
	MinBytesReadPerSec   *int64   `json:"minBytesReadPerSec"`
	MaxConnectionsPerMin *int32   `json:"maxConnectionsPerMin"`
	UseFileTransfer      *bool    `json:"useFileTransferForLargeOutput"`
	Prompt               string   `json:"prompt"`
	PromptResponse       string   `json:"promptResponse"`
	BMPStationHost       string   `json:"bmpStationHost"`
	BGPSubnets           []string `json:"bgpSubnetsToCollect"`
	HighCPU              *float32 `json:"highCpuUsage"`
	HighMemory           *float32 `json:"highMemoryUsage"`
	HighInput            *float32 `json:"highInputUtilization"`
	HighOutput           *float32 `json:"highOutputUtilization"`
	MediumCPU            *float32 `json:"mediumCpuUsage"`
	MediumMemory         *float32 `json:"mediumMemoryUsage"`
	MediumInput          *float32 `json:"mediumInputUtilization"`
	MediumOutput         *float32 `json:"mediumOutputUtilization"`
	// Clear resets properties to unset (an explicit null on the wire): the only way to unpin a device from a collector or remove a credential or option.
	Clear []string `json:"clear"`
}

// patchFor builds the partial update this definition asks for, checking the collection options against the device as it is now.
func (def classicDeviceDef) patchFor(cur *forward.ClassicDevice) (forward.ClassicDevicePatch, error) {
	patch := forward.ClassicDevicePatch{Port: def.Port, Collect: def.Collect}
	set := func(v string, to **string) {
		if v != "" {
			x := v
			*to = &x
		}
	}
	set(def.Host, &patch.Host)
	set(def.Type, &patch.Type)
	set(def.CLICredentialID, &patch.CLICredentialID)
	set(def.CLICredential2ID, &patch.CLICredential2ID)
	set(def.CLICredential3ID, &patch.CLICredential3ID)
	set(def.SNMPCredentialID, &patch.SNMPCredentialID)
	set(def.JumpServerID, &patch.JumpServerID)
	set(def.HTTPCredentialID, &patch.HTTPCredentialID)
	set(def.Note, &patch.Note)
	set(def.CollectorID, &patch.CollectorID)
	set(def.BGPTable, &patch.BGPTableType)
	set(def.BGPPeer, &patch.BGPPeerType)
	set(def.BGPFailure, &patch.BGPAndBMPFailureHandling)
	patch.EnableSNMPCollection, patch.CollectBGPAdvertisements, patch.DisableIPv6Collection, patch.FullCollectionLog = def.EnableSNMP, def.CollectBGP, def.NoIPv6, def.FullLog
	set(def.PaginationMode, &patch.PaginationMode)
	set(def.Prompt, &patch.Prompt)
	set(def.PromptResponse, &patch.PromptResponse)
	set(def.BMPStationHost, &patch.BMPStationHost)
	patch.TerminalWidth, patch.LargeRTT, patch.MinBytesReadPerSec, patch.MaxConnectionsPerMin, patch.UseFileTransferForLargeOutput = def.TerminalWidth, def.LargeRTT, def.MinBytesReadPerSec, def.MaxConnectionsPerMin, def.UseFileTransfer
	if def.BGPSubnets != nil {
		subnets := append([]string{}, def.BGPSubnets...)
		patch.BGPSubnetsToCollect = &subnets
	}
	patch.HighCPUUsage, patch.HighMemoryUsage, patch.HighInputUtilization, patch.HighOutputUtilization = def.HighCPU, def.HighMemory, def.HighInput, def.HighOutput
	patch.MediumCPUUsage, patch.MediumMemoryUsage, patch.MediumInputUtilization, patch.MediumOutputUtilization = def.MediumCPU, def.MediumMemory, def.MediumInput, def.MediumOutput
	if err := checkSessionAndThresholds(def); err != nil {
		return patch, err
	}
	if err := checkDeviceOptions(def.BGPTable, def.BGPPeer, def.BGPFailure); err != nil {
		return patch, err
	}
	if len(def.Clear) > 0 {
		for _, c := range def.Clear {
			if !deviceClearable[c] {
				return patch, fmt.Errorf("%w: clear %q: only these can be reset to unset: %s", ErrInvalidInput, c, strings.Join(clearableNames(), ", "))
			}
		}
		patch.Clear = append([]string(nil), def.Clear...)
	}
	on := def.CollectBGP
	if on == nil && cur != nil {
		on = cur.CollectBGPAdvertisements
	}
	if err := bgpNeedsAdvertisements(def.BGPTable, def.BGPPeer, on); err != nil {
		return patch, err
	}
	return patch, nil
}

// deviceClearable are the device properties a patch may reset to unset (Forward reads an explicit null as "clear it").
var deviceClearable = map[string]bool{"cliCredentialId": true, "cliCredential2Id": true, "cliCredential3Id": true, "httpCredentialId": true, "snmpCredentialId": true, "jumpServerId": true, "collectorId": true,
	"note": true, "port": true, "enableSnmpCollection": true, "collectBgpAdvertisements": true, "bgpTableType": true, "bgpPeerType": true, "bgpAndBmpFailureHandling": true, "disableIpv6Collection": true, "fullCollectionLog": true,
	"type": true, "collect": true, "paginationMode": true, "terminalWidth": true, "largeRtt": true, "minBytesReadPerSec": true, "maxConnectionsPerMin": true, "useFileTransferForLargeOutput": true, "prompt": true, "promptResponse": true, "bmpStationHost": true, "bgpSubnetsToCollect": true,
	"highCpuUsage": true, "highMemoryUsage": true, "highInputUtilization": true, "highOutputUtilization": true, "mediumCpuUsage": true, "mediumMemoryUsage": true, "mediumInputUtilization": true, "mediumOutputUtilization": true}

func clearableNames() []string {
	out := make([]string, 0, len(deviceClearable))
	for k := range deviceClearable {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// maxDeviceTestNames is how many classic devices one test call may name (the SDK's cap on a bulk start).
const maxDeviceTestNames = forward.MaxConnectivityTestDevices

// planClassicDeviceTest queues Forward's connectivity test of a small named set of classic devices. Each test connects to the device's host from its collector (the one it is pinned to, else the network's default),
// so the dry run lists them and says so; apply needs confirm equal to the number of devices. A name that is not a classic device refuses the whole call; there is no whole-network form. With definition wait_seconds the
// apply waits for each device's stored result to be newer than the moment the tests were queued and returns them.
func planClassicDeviceTest(ctx context.Context, s *fwd.Session, in editSourceInput, cur []forward.ClassicDevice) (*networkPlan, error) {
	var def struct {
		Names       []string `json:"names"`
		WaitSeconds int      `json:"wait_seconds"`
	}
	if err := decodeDefinition(in.Definition, &def, "names [the classic devices to test, at most 100], wait_seconds (wait up to this many seconds, at most 600, for fresh results)"); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, n := range def.Names {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: test takes names, the classic devices to test (a named set, never the whole network)", ErrInvalidInput)
	}
	if len(names) > maxDeviceTestNames {
		return nil, fmt.Errorf("%w: %d devices named; one call tests at most %d: test a smaller set", ErrInvalidInput, len(names), maxDeviceTestNames)
	}
	byName := map[string]forward.ClassicDevice{}
	for _, d := range cur {
		byName[d.Name] = d
	}
	var missing []string
	rows := make([]map[string]any, 0, len(names))
	for _, n := range names {
		d, ok := byName[n]
		if !ok {
			missing = append(missing, n)
			continue
		}
		collector := string(d.CollectorID)
		if collector == "" {
			collector = "the network's default collector"
		}
		rows = append(rows, map[string]any{"name": d.Name, "host": d.Host, "type": d.Type, "collector": collector})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: not classic devices of this network: %s (Forward would refuse the whole call and queue nothing)", ErrInvalidInput, strings.Join(missing, ", "))
	}
	var queuedAt time.Time
	var waitFn func(context.Context) (bool, any, error)
	before := map[string]string{} // each device's stored error before the retest, to say how many cleared
	if def.WaitSeconds > 0 {
		if sts, serr := s.DeviceTestStatuses(ctx, in.NetworkID); serr == nil {
			for _, st := range sts {
				before[st.Name] = st.ConnectivityError
			}
		}
		wait := time.Duration(min(def.WaitSeconds, 600)) * time.Second
		waitFn = func(ctx context.Context) (bool, any, error) {
			return waitForDeviceTests(ctx, s, in.NetworkID, names, &queuedAt, wait)
		}
	}
	return &networkPlan{target: fmt.Sprintf("queue a connectivity test of %d classic device(s) of network %s: each test connects to the device's host from its collector", len(names), in.NetworkID), action: "test_classic_devices",
		after: map[string]any{"devices": rows, "devices_tested": len(names)}, reversible: true, confirm: strconv.Itoa(len(names)),
		undo: "none needed: a test changes no configuration; it replaces each device's stored test result (a running test can be cancelled with Forward's stop)",
		limits: []string{
			fmt.Sprintf("this probes each named host from its collector (a login attempt per device): %d named, at most %d per call; apply needs confirm equal to %d", len(names), maxDeviceTestNames, len(names)),
			"Forward queues one task per device on the device's collector, which runs them within its own concurrency (shared with every network on that collector and with collections); an offline collector does not refuse, the tests wait",
			"Forward answers as soon as the tests are queued: a stored result keeps its old time until the new test replaces it. With wait_seconds the apply waits for results newer than the queue time; otherwise read them with inspect-collection view connectivity",
			"needs the TEST_COLLECTION_SOURCES permission",
		},
		do: func(ctx context.Context) error {
			resp, err := s.Client.ConnectivityTests.BulkStart(ctx, in.NetworkID, names)
			queuedAt = time.Now()
			if resp != nil && resp.Response != nil {
				if t, perr := http.ParseTime(resp.Header.Get("Date")); perr == nil {
					queuedAt = t // the server's clock, which the stored times use
				}
			}
			return err
		},
		verify: waitFn,
		learn: func(got any, ch *result.Change) {
			if rs, ok := got.([]map[string]any); ok {
				failedBefore, cleared, stillFailing, newlyFailing := 0, 0, 0, 0
				for _, r := range rs {
					was := before[fmt.Sprint(r["name"])]
					now := !strings.HasPrefix(fmt.Sprint(r["test"]), "failed")
					switch {
					case was != "" && now:
						failedBefore++
						cleared++
					case was != "" && !now:
						failedBefore++
						stillFailing++
					case was == "" && !now:
						newlyFailing++
					}
				}
				summary := fmt.Sprintf("%d of %d that failed before now pass, %d still fail, %d newly fail", cleared, failedBefore, stillFailing, newlyFailing)
				ch.After = map[string]any{"devices": rows, "devices_tested": len(names), "results": rs, "summary": summary}
			}
		}}, nil
}

// waitForDeviceTests polls the named classic devices until each has a stored test that ended after the tests were queued, or the wait runs out.
func waitForDeviceTests(ctx context.Context, s *fwd.Session, networkID string, names []string, queuedAt *time.Time, wait time.Duration) (bool, any, error) {
	deadline := time.Now().Add(wait)
	since := queuedAt.Add(-2 * time.Second)
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for {
		sts, err := s.DeviceTestStatuses(ctx, networkID)
		if err != nil {
			return false, nil, err
		}
		var rows []map[string]any
		fresh := true
		seen := 0
		for _, st := range sts {
			if !want[st.Name] {
				continue
			}
			seen++
			at := firstNonEmpty(st.TestEndTime, st.TestSavedAt)
			t, perr := time.Parse(time.RFC3339, at)
			isFresh := perr == nil && t.After(since)
			if !isFresh {
				fresh = false
			}
			outcome := "no error recorded"
			switch {
			case !st.TestResultPresent:
				outcome = "never tested"
			case st.ConnectivityError != "":
				outcome = "failed: " + st.ConnectivityError
				if st.ErrorPhase != "" {
					outcome += " (" + st.ErrorPhase + ")"
				}
			}
			rows = append(rows, map[string]any{"name": st.Name, "test": outcome, "tested_at": at, "newer_than_queue_time": isFresh})
		}
		if seen == len(names) && fresh {
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

// checkSessionAndThresholds refuses what Forward answers 400 to, before any request: a prompt needs its response (and may not be empty or *), the terminal width is 80 to 500, the byte and connection limits are
// positive, a high threshold is 0.75 to 1.0 and a medium one 0.5 to 1.0 and no higher than its high one.
func checkSessionAndThresholds(def classicDeviceDef) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, a...)...)
	}
	if (def.Prompt != "") != (def.PromptResponse != "") && !slices.Contains(def.Clear, "prompt") && !slices.Contains(def.Clear, "promptResponse") {
		return bad("prompt and promptResponse go together: Forward refuses one without the other")
	}
	if strings.TrimSpace(def.Prompt) == "*" {
		return bad("prompt may not be *")
	}
	if def.TerminalWidth != nil && (*def.TerminalWidth < 80 || *def.TerminalWidth > 500) {
		return bad("terminalWidth is 80 to 500, not %d", *def.TerminalWidth)
	}
	if def.MinBytesReadPerSec != nil && *def.MinBytesReadPerSec <= 0 {
		return bad("minBytesReadPerSec must be greater than 0")
	}
	if def.MaxConnectionsPerMin != nil && *def.MaxConnectionsPerMin <= 0 {
		return bad("maxConnectionsPerMin must be greater than 0")
	}
	if def.PaginationMode != "" && !slices.Contains([]string{"KEEP_UNCHANGED", "DISABLE_PAGINATION", "ENABLE_PAGINATION"}, def.PaginationMode) {
		return bad("paginationMode is KEEP_UNCHANGED, DISABLE_PAGINATION or ENABLE_PAGINATION")
	}
	pairs := []struct {
		name         string
		high, medium *float32
	}{{"cpu", def.HighCPU, def.MediumCPU}, {"memory", def.HighMemory, def.MediumMemory}, {"input utilization", def.HighInput, def.MediumInput}, {"output utilization", def.HighOutput, def.MediumOutput}}
	for _, p := range pairs {
		if p.high != nil && (*p.high < 0.75 || *p.high > 1.0) {
			return bad("the high %s threshold is a fraction from 0.75 to 1.0, not %v", p.name, *p.high)
		}
		if p.medium != nil && (*p.medium < 0.5 || *p.medium > 1.0) {
			return bad("the medium %s threshold is a fraction from 0.5 to 1.0, not %v", p.name, *p.medium)
		}
		if p.high != nil && p.medium != nil && *p.medium > *p.high {
			return bad("the medium %s threshold may not exceed the high one", p.name)
		}
	}
	return nil
}

var sourceNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// renameCheck validates a classic device rename: the new name uses letters, digits, dots, hyphens and underscores, and no other collection source of the network has it (ignoring case). A case-only change is not a rename.
func renameCheck(ctx context.Context, s *fwd.Session, networkID, oldName, newName string, classic []forward.ClassicDevice) error {
	if !sourceNameRE.MatchString(newName) {
		return fmt.Errorf("%w: a source name uses letters, digits, dots, hyphens and underscores, not %q", ErrInvalidInput, newName)
	}
	if strings.EqualFold(oldName, newName) {
		return nil
	}
	for _, d := range classic {
		if strings.EqualFold(d.Name, newName) {
			return fmt.Errorf("%w: the name %q is already used by another classic device (names are unique ignoring case across every collection source)", ErrInvalidInput, newName)
		}
	}
	if setups, _, err := s.Client.ControllerManagedSetups.List(ctx, networkID); err == nil {
		for _, st := range setups {
			names := []string{st.Name}
			for _, c := range st.Controllers {
				names = append(names, c.Name)
			}
			for _, m := range st.ManagedDevices {
				names = append(names, m.Name)
			}
			for _, n := range names {
				if strings.EqualFold(n, newName) {
					return fmt.Errorf("%w: the name %q is already used by a controller setup or one of its devices (names are unique ignoring case across every collection source)", ErrInvalidInput, newName)
				}
			}
		}
	}
	return nil
}
