package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// A view of inspect-edge (the retired skill of that name): it reports under inspect-edge.
const findPublicAddressesName = inspectEdgeName

type findPublicAddressesInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	VRF        string `json:"vrf"`
	Device     string `json:"device"`
	Role       string `json:"role"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
}

const (
	defaultPublicLimit = 50
	maxPublicLimit     = 200
	// the candidate exclude list is cut at this many CIDRs per role (more when one role is asked for)
	candidateShown        = 100
	candidateShownOneRole = 1000
)

// Roles an address can be given. A role is inferred from names and from what the model exposes, never from the address; unknown is the honest answer when the
// model gives no hint.
const (
	roleManagement     = "management"
	roleLoopback       = "loopback"
	roleCustomerFacing = "customer_facing"
	roleInternetFacing = "internet_facing"
	roleUnknown        = "unknown"
)

var publicRoleOrder = []string{roleManagement, roleLoopback, roleCustomerFacing, roleInternetFacing, roleUnknown}

// Every public address query is its own text so the shared interface-address query stays unchanged. Field names are Forward's (checked with `fwdctl nqe lint` and run on a real
// network): the device type, the interface type and the descriptions are model fields; securityZones is empty for a device that has none; managementIps lists the addresses
// Forward knows a device by.
const (
	publicAddressQuery = `foreach device in network.devices
foreach iface in device.interfaces
foreach sub in iface.subinterfaces
where isPresent(sub.ipv4)
foreach addr in sub.ipv4.addresses
select {device: device.name, deviceType: device.platform.deviceType, iface: iface.name, ifaceType: iface.interfaceType, sub: sub.name, vrf: sub.networkInstanceName, ip: addr.ip, prefixLength: addr.prefixLength, description: sub.description, ifaceDescription: iface.description}`

	securityZoneQuery = `foreach device in network.devices
foreach z in device.securityZones
foreach i in z.interfaces
select {device: device.name, zone: z.name, iface: i.ifaceName, sub: i.subIfaceName}`

	// links of firewall interfaces (the other side), and every device's type to name the other side's type; both bounded and optional
	firewallLinkQuery = `foreach device in network.devices
where device.platform.deviceType == DeviceType.FIREWALL
foreach iface in device.interfaces
foreach link in iface.links
select {device: device.name, iface: iface.name, peer: link.deviceName}`

	deviceTypeQuery = `foreach device in network.devices
select {device: device.name, deviceType: device.platform.deviceType}`

	managementIPQuery = `foreach device in network.devices
foreach ip in device.platform.managementIps
select {device: device.name, ip: ip}`
)

// nonPublicV4 mirrors Forward's own list of non-public IPv4 blocks (IpSubnetAddress.IPV4_RESERVED_SUBNETS in its source): multicast, reserved, RFC 1918, 0/8, loopback,
// link-local, carrier-grade NAT, IETF protocol, documentation, benchmarking and the retired 6to4 relay block.
var nonPublicV4 = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"224.0.0.0/4", "240.0.0.0/4", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16",
		"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "198.18.0.0/15", "192.88.99.0/24"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func isPublicV4(a netip.Addr) bool {
	if !a.Is4() {
		return false
	}
	for _, p := range nonPublicV4 {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var (
	mgmtExact     = map[string]bool{"mgmt": true, "mgt": true, "management": true, "oob": true, "fxp": true, "me": true}
	mgmtPrefixes  = []string{"management", "mgmt"}
	customerWords = map[string]bool{"inside": true, "internal": true, "customer": true, "trust": true, "lan": true, "tenant": true}
	outsideWords  = map[string]bool{"outside": true, "untrust": true, "external": true, "internet": true, "wan": true, "isp": true}
)

// tokens splits a name into lower-case alphanumeric words with trailing digits removed ("Mgmt0/1" gives "mgmt"), so a hint is a whole word and "untrust" is not "trust".
func tokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
	out := make([]string, 0, len(f))
	for _, w := range f {
		if t := strings.TrimRight(w, "0123456789"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func mgmtHint(s string) bool {
	for _, t := range tokens(s) {
		if mgmtExact[t] {
			return true
		}
		for _, p := range mgmtPrefixes {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
	}
	return false
}

func hintWords(s string, words map[string]bool) string {
	for _, t := range tokens(s) {
		if words[t] {
			return t
		}
	}
	return ""
}

type publicAddr struct {
	ifaceAddr
	DeviceType, IfaceType, Description string
	Role, Basis                        string
	Context, Linked                    string // context-name hint (external, internal, lb) and the types of devices on the other side of the link
}

// inferRole gives an address a role and says on what it rests. Order: the device's management IP, a management name on the VRF or interface, a loopback interface, and for a
// FIREWALL only a security zone, or the interface description, name or VRF saying inside or outside (the context name and the link's other side only qualify it) (both families at once is unknown). Anything else is unknown: nothing is guessed from the address.
func inferRole(a publicAddr, zone string, mgmtIP bool) (role, basis string) {
	switch {
	case mgmtIP:
		return roleManagement, "the address is one of the device's management IPs"
	case mgmtHint(a.VRF):
		return roleManagement, fmt.Sprintf("VRF %q is a management name", a.VRF)
	case mgmtHint(a.Iface) || mgmtHint(a.Sub):
		return roleManagement, "the interface name is a management name"
	case mgmtHint(a.Description):
		return roleManagement, "the interface description mentions management"
	case a.IfaceType == "IF_LOOPBACK" || strings.HasPrefix(strings.ToLower(a.Iface), "loopback") || hasLoopbackName(a.Iface):
		return roleLoopback, "the interface is a loopback"
	}
	if a.DeviceType != "FIREWALL" {
		return roleUnknown, ""
	}
	type hint struct{ src, text string }
	var hints []hint
	if zone != "" {
		hints = append(hints, hint{"security zone", zone})
	}
	hints = append(hints, hint{"interface description", a.Description}, hint{"interface name", a.Iface + " " + a.Sub}, hint{"VRF", a.VRF})
	ctx := ""
	if a.Context != "" {
		ctx = fmt.Sprintf("; the context name says %s", a.Context)
	}
	for _, h := range hints {
		in, out := hintWords(h.text, customerWords), hintWords(h.text, outsideWords)
		switch {
		case in != "" && out != "":
			return roleUnknown, ""
		case in != "":
			return roleCustomerFacing, fmt.Sprintf("%s %q says %q%s", h.src, strings.TrimSpace(h.text), in, ctx)
		case out != "":
			return roleInternetFacing, fmt.Sprintf("%s %q says %q%s", h.src, strings.TrimSpace(h.text), out, ctx)
		}
	}
	return roleUnknown, ""
}

// contextHint reads the placement a virtual firewall context's name states: the part after the last underscore (<chassis>_<agency>-ext-dmz), or the whole name, split into
// words. It says where the context sits (external or internal, behind a load balancer), not what one of its interfaces faces, so it only qualifies other evidence.
func contextHint(device string) string {
	name := device
	if i := strings.LastIndex(name, "_"); i >= 0 {
		name = name[i+1:]
	}
	var hints []string
	seen := map[string]bool{}
	for _, t := range tokens(name) {
		h := ""
		switch t {
		case "ext", "external":
			h = "external"
		case "int", "internal":
			h = "internal"
		case "lb":
			h = "lb"
		}
		if h != "" && !seen[h] {
			seen[h] = true
			hints = append(hints, h)
		}
	}
	return strings.Join(hints, "+")
}

// namePattern shortens an interface name to its shape: digits become N, so po47.600 and po48.1958 are both poN.N.
func namePattern(name string) string {
	var b strings.Builder
	prev := false
	for _, r := range name {
		if r >= '0' && r <= '9' {
			if !prev {
				b.WriteByte('N')
			}
			prev = true
			continue
		}
		prev = false
		b.WriteRune(r)
	}
	return b.String()
}

// hasLoopbackName recognises the loopback names that carry no interface type: lo, lo0, Loopback1.
func hasLoopbackName(name string) bool {
	n := strings.ToLower(name)
	if n == "lo" {
		return true
	}
	return strings.HasPrefix(n, "lo") && len(n) > 2 && strings.Trim(n[2:], "0123456789") == ""
}

// aggregateV4 returns the fewest CIDR blocks that cover exactly the given addresses: adjacent and duplicate addresses merge, and nothing outside them is included.
func aggregateV4(addrs []netip.Addr) []netip.Prefix {
	vals := make([]uint32, 0, len(addrs))
	for _, a := range addrs {
		if a.Is4() {
			b := a.As4()
			vals = append(vals, uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]))
		}
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	var out []netip.Prefix
	for i := 0; i < len(vals); {
		lo, hi := vals[i], vals[i]
		for i++; i < len(vals) && (vals[i] == hi || vals[i] == hi+1); i++ {
			hi = vals[i]
		}
		// cover [lo, hi] with the largest aligned block each step
		for cur := uint64(lo); cur <= uint64(hi); {
			size := uint64(1)
			bits := 32
			for bits > 0 && cur%(size*2) == 0 && cur+size*2-1 <= uint64(hi) {
				size *= 2
				bits--
			}
			v := uint32(cur)
			p := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), bits)
			out = append(out, p)
			cur += size
		}
	}
	return out
}

func findPublicAddresses(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in findPublicAddressesInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	in.Role = strings.ToLower(strings.TrimSpace(in.Role))
	if in.Role != "" {
		ok := false
		for _, r := range publicRoleOrder {
			ok = ok || r == in.Role
		}
		if !ok {
			return result.Result{}, fmt.Errorf("%w: role must be one of %s", ErrInvalidInput, strings.Join(publicRoleOrder, ", "))
		}
	}
	if in.Limit <= 0 {
		in.Limit = defaultPublicLimit
	}
	in.Limit = min(in.Limit, maxPublicLimit)
	if in.Offset < 0 {
		return result.Result{}, fmt.Errorf("%w: offset cannot be negative", ErrInvalidInput)
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(findPublicAddressesName, "No processed snapshot is available to answer from", cx, []string{"no processed snapshot; nothing was measured"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotID(cx)
	rows, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, sid, publicAddressQuery, maxModelRows)
	if err != nil {
		return result.Result{}, err
	}
	// the zone and management-IP reads only sharpen the role; a failed read leaves those addresses on name hints and is said so
	var notes []string
	zones := map[string]string{}
	zoneRows := 0
	if zr, _, _, zerr := s.RunNQEAll(ctx, in.NetworkID, sid, securityZoneQuery, maxModelRows); zerr != nil {
		notes = append(notes, "the security-zone read failed ("+zerr.Error()+"), so firewall roles rest on names only")
	} else {
		zoneRows = len(zr)
		for _, r := range zr {
			zones[str(r["device"])+"|"+str(r["iface"])+"|"+str(r["sub"])] = str(r["zone"])
		}
	}
	mgmt := map[string]bool{}
	mgmtRows := 0
	if mr, _, _, merr := s.RunNQEAll(ctx, in.NetworkID, sid, managementIPQuery, maxModelRows); merr != nil {
		notes = append(notes, "the management-IP read failed ("+merr.Error()+"), so management roles rest on names only")
	} else {
		mgmtRows = len(mr)
		for _, r := range mr {
			if a, perr := netip.ParseAddr(str(r["ip"])); perr == nil {
				mgmt[str(r["device"])+"|"+a.String()] = true
			}
		}
	}

	// the other side of each firewall interface: peer device types, from the topology links (bounded to firewall interfaces)
	linked := map[string]string{}
	linkRows := 0
	if lr, _, ltrunc, lerr := s.RunNQEAll(ctx, in.NetworkID, sid, firewallLinkQuery, maxModelRows); lerr != nil {
		notes = append(notes, "the firewall link read failed ("+lerr.Error()+"), so linked_to is missing")
	} else if tr, _, _, terr := s.RunNQEAll(ctx, in.NetworkID, sid, deviceTypeQuery, maxModelRows); terr != nil {
		notes = append(notes, "the device-type read failed ("+terr.Error()+"), so linked_to is missing")
	} else {
		linkRows = len(lr)
		types := map[string]string{}
		for _, r := range tr {
			types[str(r["device"])] = str(r["deviceType"])
		}
		set := map[string]map[string]bool{}
		for _, r := range lr {
			t := types[str(r["peer"])]
			switch t {
			case "":
				t = "unmodelled"
			case "FIREWALL":
				t = "FIREWALL (another context)"
			}
			k := str(r["device"]) + "|" + str(r["iface"])
			if set[k] == nil {
				set[k] = map[string]bool{}
			}
			set[k][t] = true
		}
		for k, m := range set {
			var l []string
			for t := range m {
				l = append(l, t)
			}
			sort.Strings(l)
			linked[k] = strings.Join(l, ", ")
		}
		if ltrunc {
			notes = append(notes, fmt.Sprintf("the firewall link read hit its %d-row bound", maxModelRows))
		}
	}

	var all []publicAddr
	read := 0
	seen := map[string]bool{}
	for _, r := range rows {
		a, perr := netip.ParseAddr(str(r["ip"]))
		if perr != nil || !a.Is4() {
			continue
		}
		read++
		if !isPublicV4(a) {
			continue
		}
		bits, ok := num(r["prefixLength"])
		if !ok {
			bits = 32
		}
		p, perr := a.Prefix(int(bits))
		if perr != nil {
			continue
		}
		pa := publicAddr{ifaceAddr: ifaceAddr{Device: str(r["device"]), Iface: str(r["iface"]), Sub: str(r["sub"]), VRF: str(r["vrf"]), Addr: a, Prefix: p},
			DeviceType: str(r["deviceType"]), IfaceType: str(r["ifaceType"]), Description: strings.TrimSpace(str(r["description"]))}
		if pa.Description == "" {
			pa.Description = strings.TrimSpace(str(r["ifaceDescription"]))
		}
		if pa.DeviceType == "FIREWALL" {
			pa.Context = contextHint(pa.Device)
			pa.Linked = linked[pa.Device+"|"+pa.Iface]
		}
		key := pa.Device + "|" + pa.Sub + "|" + pa.VRF + "|" + a.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		zone := zones[pa.Device+"|"+pa.Iface+"|"+pa.Sub]
		if zone == "" {
			zone = zones[pa.Device+"|"+pa.Iface+"|"]
		}
		pa.Role, pa.Basis = inferRole(pa, zone, mgmt[pa.Device+"|"+a.String()])
		all = append(all, pa)
	}
	public := len(all)

	var sel []publicAddr
	for _, a := range all {
		if (in.Device != "" && !strings.EqualFold(a.Device, in.Device)) || (in.VRF != "" && !strings.EqualFold(a.VRF, in.VRF)) || (in.Role != "" && a.Role != in.Role) {
			continue
		}
		sel = append(sel, a)
	}
	if len(sel) == 0 {
		return result.NewUnknown(findPublicAddressesName, "No public IPv4 interface address matched", cx,
			[]string{fmt.Sprintf("%d IPv4 interface addresses were read, %d of them public, and none matched the filters; an empty answer is not proof that nothing is exposed (IPv4 only, collected devices only)", read, public)},
			result.Options{NextActions: []string{"inspect-inventory"}})
	}
	rank := map[string]int{}
	for i, r := range publicRoleOrder {
		rank[r] = i
	}
	sort.SliceStable(sel, func(i, j int) bool {
		a, b := sel[i], sel[j]
		if rank[a.Role] != rank[b.Role] {
			return rank[a.Role] < rank[b.Role]
		}
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		if a.Sub != b.Sub {
			return a.Sub < b.Sub
		}
		return a.Addr.Less(b.Addr)
	})

	byRole, byType, byVRF := map[string]int{}, map[string]int{}, map[string]int{}
	devices := map[string]bool{}
	distinct := map[netip.Addr]bool{}
	roleAddrs := map[string][]netip.Addr{}
	for _, a := range sel {
		byRole[a.Role]++
		t := a.DeviceType
		if t == "" {
			t = "(not reported)"
		}
		byType[t]++
		v := a.VRF
		if v == "" {
			v = "default"
		}
		byVRF[v]++
		devices[a.Device] = true
		distinct[a.Addr] = true
		roleAddrs[a.Role] = append(roleAddrs[a.Role], a.Addr)
	}

	// candidate exclude blocks per role: exact aggregation of the addresses. Internet-facing and unknown are never offered: excluding them would hide real exposure or
	// bulk-exclude addresses nobody has looked at.
	shown := candidateShown
	if in.Role != "" {
		shown = candidateShownOneRole
	}
	candidates := map[string]any{}
	var allCIDRs []netip.Prefix
	for _, role := range []string{roleManagement, roleLoopback, roleCustomerFacing} {
		addrs := roleAddrs[role]
		if len(addrs) == 0 {
			continue
		}
		cidrs := aggregateV4(addrs)
		list := make([]string, 0, min(len(cidrs), shown))
		for i, c := range cidrs {
			if i >= shown {
				break
			}
			list = append(list, c.String())
		}
		d := map[string]any{"addresses": len(addrs), "cidr_count": len(cidrs), "cidrs": list, "ownership_not_verified": true}
		allCIDRs = append(allCIDRs, cidrs...)
		if len(cidrs) > shown {
			d["cidrs_truncated"] = fmt.Sprintf("first %d of %d shown; narrow with role to see up to %d", shown, len(cidrs), candidateShownOneRole)
		}
		candidates[role] = d
	}

	// firewall addresses whose role stayed unknown: a separate list to review, never part of candidate_exclude
	var fwUnknown []publicAddr
	var unknownAll []publicAddr
	for _, a := range sel {
		if a.Role != roleUnknown {
			continue
		}
		unknownAll = append(unknownAll, a)
		if a.DeviceType == "FIREWALL" {
			fwUnknown = append(fwUnknown, a)
		}
	}
	var review map[string]any
	if len(fwUnknown) > 0 {
		var addrs []netip.Addr
		for _, a := range fwUnknown {
			addrs = append(addrs, a.Addr)
		}
		cidrs := aggregateV4(addrs)
		list := make([]string, 0, min(len(cidrs), shown))
		for i, c := range cidrs {
			if i >= shown {
				break
			}
			list = append(list, c.String())
		}
		review = map[string]any{"label": "needs review: do not exclude blindly. These are public addresses on firewalls for which nothing in the model says inside, outside or management; they may be customer-facing, internet-facing or neither.",
			"addresses": len(fwUnknown), "cidr_count": len(cidrs), "cidrs": list, "ownership_not_verified": true, "by_group": unknownGroups(fwUnknown, 15)}
		if len(cidrs) > shown {
			review["cidrs_truncated"] = fmt.Sprintf("first %d of %d shown; filter with role unknown and a device to see more", shown, len(cidrs))
		}
	}

	total := len(sel)
	if in.Offset >= total {
		return result.NewUnknown(findPublicAddressesName, fmt.Sprintf("Offset %d is past the %d matching addresses", in.Offset, total), cx, []string{"nothing to show at that offset"}, result.Options{})
	}
	end := min(in.Offset+in.Limit, total)
	page := make([]map[string]any, 0, end-in.Offset)
	for _, a := range sel[in.Offset:end] {
		m := a.row()
		if a.DeviceType != "" {
			m["device_type"] = a.DeviceType
		}
		m["role"] = a.Role
		if a.Basis != "" {
			m["role_basis"] = a.Basis
		}
		if a.IfaceType != "" {
			m["interface_type"] = a.IfaceType
		}
		if a.Context != "" {
			m["context_hint"] = a.Context
		}
		if a.Linked != "" {
			m["linked_to"] = a.Linked
		}
		if a.Description != "" {
			d := a.Description
			if len(d) > 80 {
				d = d[:80] + "..."
			}
			m["description"] = d
		}
		page = append(page, m)
	}

	limits := []string{
		"public means an IPv4 address outside Forward's own non-public list (private, shared/carrier NAT, loopback, link-local, documentation, benchmarking, multicast, reserved); IPv4 only; collected devices only; one row per interface address, so an address on several interfaces counts once per interface",
		"role is inferred, never measured: management = the device's management IP, or a management name (mgmt, mgt, management, oob, fxp, me) on the VRF, interface or description; loopback = a loopback interface; customer_facing and internet_facing exist for FIREWALL devices only and only when a security zone, an interface description or an interface name says inside/internal/customer/trust/lan/tenant or outside/untrust/external/internet/wan/isp; everything else is unknown. Read role_basis on each row",
		"candidate_exclude is a candidate set for the person to review, per role, aggregated exactly (no address outside the listed ones is covered). internet_facing and unknown addresses are never offered. The exclude list is prefix-based (no per-interface or per-device exclusion) and applies from the next processed snapshot; per Forward's source it only changes which public space the internet node owns, and an address on a collected interface is located there first, so excluding it need not change a device's internet-addressable flag: read plan-synthetic-device reference/exposure.md and trace before and after",
		"when this view, the devices it lists or a device flagged internet addressable are discussed: " + addressableCaveat,
	}
	if zoneRows == 0 {
		limits = append(limits, "the model lists no security zone for any device. Forward models zones only for the firewall platforms that have them (for example FTD managed by FMC, Check Point); a Cisco ASA has no zone, its nameif and security level are not NQE fields, and most ASA firewalls here are virtual contexts. So a firewall's customer-facing interface is told only from its description, interface name or VRF saying inside or outside; the context name (ext, int, lb) and the link's other side (linked_to) qualify a row and are not used to assign a role; the rest stay unknown, see firewall_role_unknown")
	}
	limits = append(limits, "ownership_not_verified: this skill does NOT check who owns a public prefix (no whois or registry lookup), so a candidate may be space the customer does not own, such as 2.x.x.x or other public blocks used as if private; excluding space the customer does not own only hides it. Read prefix_groups and largest_aggregates to spot odd ones")
	var omitted []result.Omission
	if in.Offset > 0 || end < total {
		omitted = append(omitted, result.Omission{What: "addresses", Total: total, Shown: end - in.Offset, From: in.Offset, Paged: true,
			Next: joinNext(pageNext(end, total), fmt.Sprintf("limit up to %d", maxPublicLimit), fmt.Sprintf("rows are by role, then device; the summary and the candidate set cover all %d", total))})
	}
	if trunc {
		limits = append(limits, fmt.Sprintf("the interface-address read hit its %d-row bound, so addresses may be missing", maxModelRows))
	}
	limits = append(limits, notes...)

	pg, largest := prefixGroups(allCIDRs)
	verify := verifyPlan(in.NetworkID, string(snap.ID), allCIDRs, sel)
	next := []string{"inspect-vulnerabilities", "investigate-reachability", "edit-internet-exclusions", "edit-advanced-reachability"}
	d := map[string]any{
		"snapshot_id": string(snap.ID), "interface_addresses_read": read, "public_addresses": public, "matching": total, "distinct_addresses": len(distinct), "devices": len(devices),
		"by_role": countsTop(byRole, 8), "by_device_type": countsTop(byType, 16), "by_vrf": countsTop(byVRF, 15),
		"candidate_exclude": candidates, "prefix_groups": pg, "largest_aggregates": largest, "offset": in.Offset, "addresses": page,
		"unknown_by_group": unknownGroups(unknownAll, 20), "verify": verify,
		"model_reads": map[string]any{"security_zone_rows": zoneRows, "management_ip_rows": mgmtRows, "firewall_link_rows": linkRows},
	}
	if review != nil {
		d["firewall_role_unknown"] = review
	}
	finding := fmt.Sprintf("%d public IPv4 interface address(es) on %d device(s) (%d distinct addresses; snapshot %s): %s", total, len(devices), len(distinct), snap.ID, roleSummary(byRole))
	return result.Build(findPublicAddressesName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: next,
		Evidence:    []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", fwd.SnapshotIDPtr(snap), d, finding)}})
}

func roleSummary(by map[string]int) string {
	var parts []string
	for _, r := range publicRoleOrder {
		if n := by[r]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, r))
		}
	}
	return strings.Join(parts, ", ")
}

// unknownGroups groups addresses by device type, VRF and interface-name pattern (digits become N), with the top descriptions of each group and the context hint, so the person
// sees what the unknown rows are. Largest groups first, at most max.
func unknownGroups(rows []publicAddr, max int) []map[string]any {
	type g struct {
		devType, vrf, pattern string
		n                     int
		ctx                   map[string]int
		desc                  map[string]int
		addrs                 []netip.Addr
	}
	groups := map[string]*g{}
	for _, a := range rows {
		vrf := a.VRF
		if vrf == "" {
			vrf = "default"
		}
		t := a.DeviceType
		if t == "" {
			t = "(not reported)"
		}
		k := t + "|" + vrf + "|" + namePattern(a.Iface)
		x := groups[k]
		if x == nil {
			x = &g{devType: t, vrf: vrf, pattern: namePattern(a.Iface), ctx: map[string]int{}, desc: map[string]int{}}
			groups[k] = x
		}
		x.n++
		x.addrs = append(x.addrs, a.Addr)
		if a.Context != "" {
			x.ctx[a.Context]++
		}
		if a.Description != "" {
			d := strings.ToLower(a.Description)
			if len(d) > 40 {
				d = d[:40]
			}
			x.desc[d]++
		}
	}
	all := make([]*g, 0, len(groups))
	for _, x := range groups {
		all = append(all, x)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].devType+all[i].vrf+all[i].pattern < all[j].devType+all[j].vrf+all[j].pattern
	})
	out := []map[string]any{}
	for i, x := range all {
		if i >= max {
			break
		}
		m := map[string]any{"device_type": x.devType, "vrf": x.vrf, "interface_pattern": x.pattern, "addresses": x.n, "cidr_count": len(aggregateV4(x.addrs))}
		if len(x.desc) > 0 {
			m["top_descriptions"] = countsTop(x.desc, 3)
		}
		if len(x.ctx) > 0 {
			m["context_hints"] = countsTop(x.ctx, 3)
		}
		out = append(out, m)
	}
	return out
}

// prefixGroups reports how a candidate set spreads over /8 and /16 blocks and its largest aggregates, so odd space (a /8 that is not the customer's) stands out. It is arithmetic on
// the candidate set only; nothing is looked up.
func prefixGroups(cidrs []netip.Prefix) (map[string]any, []string) {
	s8, s16 := map[string]int{}, map[string]int{}
	for _, c := range cidrs {
		b := c.Addr().As4()
		n := 1 << (32 - c.Bits())
		s8[fmt.Sprintf("%d.0.0.0/8", b[0])] += n
		s16[fmt.Sprintf("%d.%d.0.0/16", b[0], b[1])] += n
	}
	big := append([]netip.Prefix(nil), cidrs...)
	sort.SliceStable(big, func(i, j int) bool { return big[i].Bits() < big[j].Bits() })
	var largest []string
	for i, c := range big {
		if i >= 5 {
			break
		}
		largest = append(largest, c.String())
	}
	return map[string]any{"slash8": countsTop(s8, 20), "slash16": countsTop(s16, 20), "unit": "addresses covered by the candidate CIDRs"}, largest
}

// verifyPlan is the whole before and after flow as concrete calls: advanced reachability PROCESSED first, the internet-addressable count and a trace from the internet before and
// after the exclusion list is set, and a reminder that the list applies from the next snapshot and may not change the flag for addresses on collected interfaces.
func verifyPlan(networkID, snapshotID string, cidrs []netip.Prefix, sel []publicAddr) map[string]any {
	probe := ""
	if len(cidrs) > 0 {
		probe = cidrs[0].Addr().String()
	} else if len(sel) > 0 {
		probe = sel[0].Addr.String()
	}
	steps := []map[string]any{
		{"step": 1, "skill": "inspect-snapshots", "input": map[string]any{"network_id": networkID, "snapshot_id": snapshotID}, "check": "advanced_reachability.state is PROCESSED; if UNPROCESSED run edit-advanced-reachability (dry run first, asynchronous, compute-heavy) and wait"},
		{"step": 2, "skill": "inspect-vulnerabilities", "input": map[string]any{"network_id": networkID, "snapshot_id": snapshotID, "internet_addressable": true}, "check": "record the exposed device count and list BEFORE any exclusion"},
		{"step": 3, "skill": "investigate-reachability", "input": map[string]any{"network_id": networkID, "snapshot_id": snapshotID, "from": "internet", "dst_ip": probe}, "check": "record classification and last_hop for one address BEFORE"},
		{"step": 4, "skill": "edit-internet-exclusions", "input": map[string]any{"network_id": networkID, "add": []string{"<reviewed CIDRs>"}}, "check": "dry run first; the list applies from the next processed snapshot (or backdate_snapshot_id on the latest snapshot only)"},
		{"step": 5, "skill": "inspect-snapshots", "check": "on the snapshot that reflects the change, advanced_reachability must be PROCESSED again (a new or reprocessed snapshot starts UNPROCESSED)"},
		{"step": 6, "skill": "inspect-vulnerabilities", "check": "the same count and device list AFTER; compare with step 2"},
		{"step": 7, "skill": "investigate-reachability", "check": "the same trace AFTER; compare with step 3"},
	}
	return map[string]any{"steps": steps, "note": "an exclusion keeps a prefix off the internet node and may not change the flag of a device whose collected interface carries the address (plan-synthetic-device reference/exposure.md): an unchanged count after the change is a finding, not a failure of the procedure"}
}
