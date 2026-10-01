package skills

import (
	"fmt"
	"sort"
	"strings"
)

// SynthOptions chooses how the internet-connection query is derived from the edge analysis.
type SynthOptions struct {
	Discovery       string   // interfaceAddresses (default), ipRoutes, bgpRoutes or none
	IncludeUnlikely bool     // every unowned exit, not only the likely internet edges
	Subnets         []string // written on every row; required for none
	NetworkID       string
	VRF, Device     string // the filters the analysis ran with (stated in the header)
}

// SynthRow is one InetConnection row: a (device, egress) pair of a selected exit.
type SynthRow struct {
	Device, Uplink, Gateway string
	VLAN                    int // 0: untagged (the column is omitted, never written as 0)
	Peers                   []string
	Claims                  []Claimant
	NextHops                []string
	VRFs                    []string
	Likely                  bool
}

// SynthResult is the generated query and what it holds.
type SynthResult struct {
	Source   string
	Rows     []SynthRow
	Skipped  []string // exits left out and why
	Warnings []string
}

// SynthesizeInternet writes an NQE query whose rows are the internet connections for the exits of an edge analysis: one InetConnection per (device, egress), the uplink the parent
// port of a subinterface and the gateway the subinterface, with the VLAN read from the subinterface name.
func SynthesizeInternet(an *EdgeAnalysis, o SynthOptions) (*SynthResult, error) {
	switch o.Discovery {
	case "":
		o.Discovery = "interfaceAddresses"
	case "interfaceAddresses", "ipRoutes", "bgpRoutes", "none":
	default:
		return nil, fmt.Errorf("unknown discovery %q; one of interfaceAddresses, ipRoutes, bgpRoutes, none", o.Discovery)
	}
	for _, sn := range o.Subnets {
		if strings.ContainsAny(sn, "\"\\\n ") || !strings.Contains(sn, "/") {
			return nil, fmt.Errorf("subnet %q is not a CIDR prefix", sn)
		}
	}
	if o.Discovery == "none" && len(o.Subnets) == 0 {
		return nil, fmt.Errorf("discovery none advertises exactly the subnets listed and Forward requires them to be non-empty: pass --subnets, or choose interfaceAddresses, ipRoutes or bgpRoutes")
	}
	res := &SynthResult{}
	byKey := map[string]*SynthRow{}
	var order []string
	for _, g := range an.Exits {
		if !g.Likely() && !o.IncludeUnlikely {
			continue
		}
		ports := g.Ports()
		if len(ports) == 0 {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s next hop %s: no resolved egress interface on %s", g.VRF, g.NextHop, joinTop(keys(g.devices), 4)))
			continue
		}
		for _, p := range ports {
			k := p.Device + "\x00" + p.Interface
			r := byKey[k]
			if r == nil {
				r = &SynthRow{Device: p.Device, Uplink: p.Interface, Claims: p.Claims}
				if v, ok := subVLAN(p.Interface); ok {
					r.Uplink, r.Gateway, r.VLAN = p.Interface[:strings.LastIndex(p.Interface, ".")], p.Interface, v
				}
				byKey[k] = r
				order = append(order, k)
			}
			r.Likely = r.Likely || g.Likely()
			r.NextHops = appendUnique(r.NextHops, g.NextHop)
			r.VRFs = appendUnique(r.VRFs, g.VRF)
			for _, pe := range g.Peers() {
				if pe.Device == p.Device && pe.EBGP {
					r.Peers = appendUnique(r.Peers, pe.Peer)
				}
			}
		}
	}
	sort.Strings(order)
	for _, k := range order {
		res.Rows = append(res.Rows, *byKey[k])
	}
	if len(res.Rows) == 0 {
		return nil, fmt.Errorf("no exit to write: the analysis found no %s (snapshot %s)%s", map[bool]string{true: "unowned exit with a resolved egress", false: "likely internet edge; --include-unlikely adds every unowned exit"}[o.IncludeUnlikely], an.SnapshotID, skippedText(res.Skipped))
	}
	if o.Discovery == "bgpRoutes" {
		var missing []string
		for _, r := range res.Rows {
			if len(r.Peers) == 0 {
				missing = append(missing, r.Device+" "+r.Gateway+r.noGateway())
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("bgpRoutes needs the peer addresses and no unmodelled eBGP peer was found for %s; choose another discovery or write the peerIps by hand", strings.Join(missing, ", "))
		}
	}
	res.Warnings = synthWarnings(an, o, res)
	res.Source = writeInetQuery(an, o, res)
	return res, nil
}

func (r SynthRow) noGateway() string {
	if r.Gateway == "" {
		return r.Uplink
	}
	return ""
}

func appendUnique(l []string, v string) []string {
	for _, x := range l {
		if x == v {
			return l
		}
	}
	return append(l, v)
}

func skippedText(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return "; left out: " + strings.Join(s, "; ")
}

func synthWarnings(an *EdgeAnalysis, o SynthOptions, res *SynthResult) []string {
	w := []string{
		"a connection on an uplink and VLAN that another synthetic node already claims is a double claim: Forward's behaviour for it is not documented and it is a likely mistake. Attach this query to a node only after the claimants listed above are removed, or point the node that already claims it at this query instead",
	}
	switch o.Discovery {
	case "bgpRoutes":
		w = append(w, "bgpRoutes: the advertised-prefix counts are unreconciled (measured on one network, the BGP neighbor's advertised_prefixes and the distinct prefixes of the Adj-RIB-Out disagree by hundreds of prefixes), so do not derive an exclude-subnets list from them blindly; run inspect-bgp-neighbors and compare before excluding anything")
	case "ipRoutes":
		w = append(w, "ipRoutes with advertisesDefaultRoute false: the connection owns the public subnets the gateway forwards out a port other than the uplink; the default route is never counted")
	case "interfaceAddresses":
		w = append(w, "interfaceAddresses: the connection owns only the gateway interface's own addresses; any other public space you route internally keeps landing on the internet node unless it is discovered another way or excluded")
	case "none":
		w = append(w, "none: the connection owns exactly the subnets given on the command line")
	}
	return w
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func ifaceRef(device, iface string) string {
	return fmt.Sprintf("{ deviceName: %s, interfaceName: %s }", quote(device), quote(iface))
}

func writeInetQuery(an *EdgeAnalysis, o SynthOptions, res *SynthResult) string {
	var b strings.Builder
	c := func(format string, a ...any) {
		if format == "" {
			b.WriteString("//\n")
			return
		}
		fmt.Fprintf(&b, "// "+format+"\n", a...)
	}
	snapTime := ""
	if an.Context.SnapshotTime != nil {
		snapTime = " (" + *an.Context.SnapshotTime + ")"
	}
	c("Internet connections derived by `fwdctl nqe synthesize internet` from the model of network %s, snapshot %s%s.", o.NetworkID, an.SnapshotID, snapTime)
	filt := "every VRF and device"
	switch {
	case o.VRF != "" && o.Device != "":
		filt = "VRF " + o.VRF + ", device " + o.Device
	case o.VRF != "":
		filt = "VRF " + o.VRF
	case o.Device != "":
		filt = "device " + o.Device
	}
	sel := "likely internet edges"
	if o.IncludeUnlikely {
		sel = "every unowned exit (--include-unlikely)"
	}
	c("Read: IPv4 default-route next hops of %s that no modelled device owns, selecting %s. %d row(s), one per device and egress interface.", filt, sel, len(res.Rows))
	c("A likely internet edge is an unowned public next hop with an eBGP session to a peer the model does not hold in the next hop's connected subnet; it does not prove the far side is the internet.")
	c("")
	c("Rows (uplinkInterface is the parent port of a subinterface, gatewayInterface the subinterface, vlan the number after its dot):")
	for _, r := range res.Rows {
		why := "unowned next hop " + strings.Join(r.NextHops, ", ") + " in VRF " + strings.Join(r.VRFs, ", ")
		if r.Likely {
			why += ", likely internet edge"
		} else {
			why += ", NOT a likely internet edge (no eBGP session to an unmodelled peer, or a non-public next hop)"
		}
		port := r.Uplink
		if r.Gateway != "" {
			port = r.Gateway
		}
		c("  %s %s: %s", r.Device, port, why)
		for _, cl := range r.Claims {
			c("    ALREADY CLAIMED by %s %q (%s, uplink %s%s): attaching this row there as well is a double claim", cl.Kind, cl.Node, cl.Source, cl.Uplink, map[bool]string{true: ", gateway " + cl.Gateway, false: ""}[cl.Gateway != ""])
		}
	}
	for _, s := range res.Skipped {
		c("  left out: %s", s)
	}
	c("")
	switch o.Discovery {
	case "interfaceAddresses":
		c("Discovery: interfaceAddresses. The connection owns only the gateway interface's own addresses.")
	case "ipRoutes":
		c("Discovery: ipRoutes({advertisesDefaultRoute: false}). The connection owns the public subnets the gateway forwards out a port other than the uplink; the default route is not counted.")
	case "bgpRoutes":
		c("Discovery: bgpRoutes. The connection owns what the gateway advertises to the listed eBGP peers (the Adj-RIB-Out); peerIps are the unmodelled eBGP peers found in each connected subnet.")
	case "none":
		c("Discovery: none. The connection owns exactly the subnets listed; Forward requires them to be non-empty.")
	}
	c("")
	c("WARNINGS")
	for _, w := range res.Warnings {
		c("- %s", w)
	}
	c("- Derived from one snapshot: once saved and attached Forward re-runs the query on each processed snapshot, but the evidence above is not re-read, so regenerate it after the edge changes.")
	b.WriteString("\n")
	b.WriteString("// Helper function for an empty list of subnets.\nemptySubnets =\n  foreach x in fromTo(1, 0)\n  select null : IpSubnet;\n\n")
	b.WriteString("// Helper function for an empty list of IfaceReference records.\nemptyInterfaces =\n  foreach x in fromTo(1, 0)\n  select null : IfaceReference;\n\n")
	subnets := "emptySubnets"
	if len(o.Subnets) > 0 {
		var qs []string
		for _, s := range o.Subnets {
			qs = append(qs, "ipSubnet("+quote(s)+")")
		}
		subnets = "[" + strings.Join(qs, ", ") + "]"
	}
	var names []string
	for i, r := range res.Rows {
		name := fmt.Sprintf("connection%d", i+1)
		names = append(names, name)
		fmt.Fprintf(&b, "%s : InetConnection =\n  { uplinkInterface: %s,\n", name, ifaceRef(r.Device, r.Uplink))
		if r.Gateway != "" {
			fmt.Fprintf(&b, "    gatewayInterface: %s,\n", ifaceRef(r.Device, r.Gateway))
			fmt.Fprintf(&b, "    vlan: %d,\n", r.VLAN)
		}
		method := map[string]string{"interfaceAddresses": "SubnetDiscoveryMethod.interfaceAddresses", "none": "SubnetDiscoveryMethod.none",
			"ipRoutes": "SubnetDiscoveryMethod.ipRoutes({ advertisesDefaultRoute: false })"}[o.Discovery]
		if o.Discovery == "bgpRoutes" {
			var ps []string
			for _, p := range r.Peers {
				ps = append(ps, "ipAddress("+quote(p)+")")
			}
			sort.Strings(ps)
			method = "SubnetDiscoveryMethod.bgpRoutes({ peerIps: [" + strings.Join(ps, ", ") + "] })"
		}
		fmt.Fprintf(&b, "    subnetDiscoveryMethod: %s,\n    subnets: %s,\n    backdoorInterfaces: emptyInterfaces\n  };\n\n", method, subnets)
	}
	b.WriteString("@query\nconnections : List<InetConnection> =\n  [")
	b.WriteString(strings.Join(names, ",\n   "))
	b.WriteString("];\n")
	return b.String()
}
