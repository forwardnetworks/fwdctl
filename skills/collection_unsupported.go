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
	idx := map[string]map[string]any{}
	for _, r := range kept {
		if str(r["type"]) == unsupportedType {
			idx[str(r["device"])] = r
		}
	}
	if len(idx) == 0 {
		return nil, nil
	}
	var limits []string
	disc := map[string]string{}
	if sts, err := s.DeviceTestStatuses(ctx, networkID); err != nil {
		limits = append(limits, fmt.Sprintf("the discovered type could not be read (%v)", err))
	} else {
		for _, st := range sts {
			if st.DiscoveredType != "" && st.DiscoveredType != "unknown" {
				disc[st.Name] = st.DiscoveredType
			}
		}
	}
	by := map[string]int{}
	for name, r := range idx {
		plat := strings.TrimSpace(str(r["vendor"]) + " " + str(r["os"]))
		by[plat]++
		r["reason"] = "Forward recognised this device and cannot collect this device type"
		for _, k := range unsupportedKinds {
			if strings.Contains(strings.ToLower(str(r["os"])+" "+str(r["vendor"])), k.osContains) {
				r["likely"] = k.likely
				break
			}
		}
		if d, ok := disc[name]; ok {
			r["discovered_type"] = d
		}
	}
	limits = append(limits,
		fmt.Sprintf("%d device(s) failed as %s: Forward recognised the device and cannot collect it, so it fails every collection until it is removed. Remove or exclude it from collection (or contact Forward support if you think the type should be supported); no setting makes it collect", len(idx), unsupportedType),
		"`likely` is a hint from the platform and Forward's explicit unsupported-device detectors; it is not confirmed for each device (a device name is a clue, not proof). Confirm from the model or the device's own output")
	return by, limits
}
