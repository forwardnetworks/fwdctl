package skills

import (
	"context"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// unsupportedKinds are the devices Forward's discovery names as unsupported (the explicit detectors in its device-type discovery configuration, read by a peer session; not re-read here). A match on the output of a device whose
// platform is shown here makes Forward fail it as UNSUPPORTED_DEVICE_TYPE. The platform is only a hint to what it is: the platform comes from the failed device record, and nothing here reads the device's output.
var unsupportedKinds = []struct{ osContains, likely string }{
	{"nxos", "a Cisco MDS Fibre Channel SAN switch (Forward's NX-OS discovery refuses output that says \"cisco MDS\")"},
	{"pan", "a Palo Alto Panorama or M-series management appliance"},
	{"f5", "an F5 BIG-IQ"},
	{"brocade", "a Brocade MLXe"},
	{"extreme", "an Extreme EXOS device"},
	{"exos", "an Extreme EXOS device"},
}

const unsupportedType = "UNSUPPORTED_DEVICE_TYPE"

// explainUnsupported adds, to each failed-device row of type UNSUPPORTED_DEVICE_TYPE, the reason, the platform's likely identity and the type its connectivity test discovered, and returns the counts by platform and the limits that
// say what to do. It changes nothing in Forward.
func explainUnsupported(ctx context.Context, s *fwd.Session, networkID string, kept []map[string]any) (map[string]int, []string) {
	if len(kept) == 0 {
		return nil, nil
	}
	var limits []string
	type test struct{ typ, errName, phase, at string }
	tests := map[string]test{}
	if sts, err := s.DeviceTestStatuses(ctx, networkID); err != nil {
		limits = append(limits, fmt.Sprintf("the stored connectivity test results could not be read (%v)", err))
	} else {
		for _, st := range sts {
			t := test{errName: st.ConnectivityError, phase: st.ErrorPhase, at: firstNonEmpty(st.TestSavedAt, st.TestEndTime)}
			if st.DiscoveredType != "" && st.DiscoveredType != "unknown" {
				t.typ = st.DiscoveredType
			}
			tests[st.Name] = t
		}
	}
	// who created each classic device and when (a device with no attribution reads back empty)
	if classic, cerr := s.ClassicDevices(ctx, networkID); cerr == nil {
		made := map[string]struct{ by, at string }{}
		for _, d := range classic {
			made[d.Name] = struct{ by, at string }{d.CreatedBy, d.CreatedAt}
		}
		for _, r := range kept {
			if m, ok := made[str(r["device"])]; ok {
				if m.by != "" {
					r["created_by"] = m.by
				}
				if m.at != "" {
					r["created_at"] = m.at
				}
			}
		}
	}
	by := map[string]int{}
	unsupported, incomplete, unreachable := 0, 0, 0
	for _, r := range kept {
		name := str(r["device"])
		t, tested := tests[name]
		if tested && t.errName != "" {
			r["connectivity_error"], r["error_phase"] = t.errName, t.phase
			if t.at != "" {
				r["tested_at"] = t.at
			}
		}
		switch str(r["type"]) {
		case unsupportedType:
			unsupported++
			plat := strings.TrimSpace(str(r["vendor"]) + " " + str(r["os"]))
			by[plat]++
			r["reason"] = "Forward recognised this device and cannot collect this device type"
			for _, k := range unsupportedKinds {
				if strings.Contains(strings.ToLower(str(r["os"])+" "+str(r["vendor"])), k.osContains) {
					r["likely"] = k.likely
					break
				}
			}
			if tested && t.typ != "" {
				r["discovered_type"] = t.typ
			}
		case "INCOMPLETE_SETUP":
			incomplete++
			r["reason"] = "Forward could not resolve this device's type or credentials"
			if tested && unreachableTest(t.errName) {
				unreachable++
				r["reason"] = fmt.Sprintf("the device type and credentials are unknown because the host could not be reached: its last connectivity test failed with %s in %s", t.errName, t.phase)
			}
			if tested && t.typ != "" {
				r["discovered_type"] = t.typ
			}
		}
	}
	if incomplete > 0 {
		limits = append(limits, fmt.Sprintf("%d device(s) failed as INCOMPLETE_SETUP (Forward's text: the device type and/or credentials are unknown); %d of them have a stored connectivity test that failed to reach the host (ping or connection), so the cause is reachability, not missing setup. A device with no stored test result is not explained", incomplete, unreachable))
	}
	if unsupported > 0 {
		limits = append(limits,
			fmt.Sprintf("%d device(s) failed as %s: Forward recognised the device and cannot collect it, so it fails every collection until it is removed. Remove or exclude it from collection (or contact Forward support if you think the type should be supported); no setting makes it collect", unsupported, unsupportedType),
			"`likely` is a hint from the platform and Forward's explicit unsupported-device detectors; it is not confirmed for each device (a device name is a clue, not proof). Confirm from the model or the device's own output")
	}
	if len(by) == 0 {
		by = nil
	}
	return by, limits
}

// unreachableTest says a stored connectivity-test error means the host could not be reached.
func unreachableTest(errName string) bool {
	switch errName {
	case "PING_FAILED", "TIMED_OUT", "CONNECTION_REFUSED", "CONNECTION_TIMED_OUT", "HOST_UNREACHABLE", "NO_ROUTE_TO_HOST":
		return true
	}
	return false
}
