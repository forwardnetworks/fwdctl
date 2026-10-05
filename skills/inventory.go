package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const inventoryName = "inspect-inventory"

func init() { Register(inventoryName, inspectInventory) }

type inventoryInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	Kind       string `json:"kind"`
	Device     string `json:"device"`
	Account    string `json:"account"`
	Name       string `json:"name"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	// Vendor keeps the devices of one vendor (kinds devices and security_rules_experimental): Forward's vendor name, case-insensitive, such as FORTINET or CISCO.
	Vendor string `json:"vendor"`
	// CompareToSnapshotID (kind devices) lists the devices added and removed since another snapshot.
	CompareToSnapshotID string `json:"compare_to_snapshot_id"`
	// IPs belongs to kind ip_owner: the IPv4 addresses whose owning interface to find.
	IPs []string `json:"ips"`
}

// Every filter is a query parameter; "" means no filter. Nothing from the caller is spliced into the query text.
const (
	// the security rules model is experimental (PAN-OS and FortiOS; FortiOS native rules are off unless the org property NQE_SECURITY_RULES_FORTIOS is true): counts only, per device
	// and scope. invSecurityRules uses the fields of a current build; invSecurityRulesCore only those every build with the model has.
	invSecurityRules = `@query
query(deviceName: String, nameGlob: String, vendorName: String) =
foreach device in network.devices
where (deviceName == "" || device.name == deviceName) && (nameGlob == "" || matches(toLowerCase(device.name), nameGlob)) && (vendorName == "" || toString(device.platform.vendor) == vendorName) && isPresent(device.securityPolicy)
foreach scope in device.securityPolicy.scopes
select {
  Device: device.name,
  Vendor: device.platform.vendor,
  Scope: scope.id,
  Rulebases: length(scope.rulebases),
  Rules: sum(foreach rb in scope.rulebases select length(rb.rules)),
  "Address objects": length(scope.addressObjects),
  "Dynamic address objects": length(scope.dynamicAddressObjects),
  Regions: length(scope.regions),
  "Zone objects": length(scope.zoneObjects),
  "User objects": length(scope.userObjects),
  "User groups": length(scope.userGroups)
}
order by Rules desc, "Address objects" desc, Device asc natural;`
	invSecurityRulesCore = `@query
query(deviceName: String, nameGlob: String, vendorName: String) =
foreach device in network.devices
where (deviceName == "" || device.name == deviceName) && (nameGlob == "" || matches(toLowerCase(device.name), nameGlob)) && (vendorName == "" || toString(device.platform.vendor) == vendorName) && isPresent(device.securityPolicy)
foreach scope in device.securityPolicy.scopes
select {
  Device: device.name,
  Vendor: device.platform.vendor,
  Scope: scope.id,
  Rulebases: length(scope.rulebases),
  Rules: sum(foreach rb in scope.rulebases select length(rb.rules)),
  "Address objects": length(scope.addressObjects),
  Regions: length(scope.regions),
  "User objects": length(scope.userObjects)
}
order by Rules desc, "Address objects" desc, Device asc natural;`

	invDevices = `@query
query(deviceName: String, nameGlob: String, vendorName: String) =
foreach device in network.devices
where (deviceName == "" || device.name == deviceName) && (nameGlob == "" || matches(toLowerCase(device.name), nameGlob)) && (vendorName == "" || toString(device.platform.vendor) == vendorName)
select {
  Device: device.name,
  Vendor: device.platform.vendor,
  OS: device.platform.os,
  "OS version": device.platform.osVersion,
  Model: device.platform.model,
  Type: device.platform.deviceType,
  Location: device.locationName,
  Tags: device.tagNames
}
order by Device asc natural;
`
	// invInterfaces lists the addresses on subinterfaces AND on routed-VLAN (SVI) interfaces, which Forward models under routedVlan, not under a subinterface, and the VRFs they are in
	invInterfaces = `@query
query(deviceName: String, ifaceName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach interface in device.interfaces
where ifaceName == "" || interface.name == ifaceName
let subAddresses = (foreach sub in interface.subinterfaces
                    foreach address in sub.ipv4.addresses
                    select address.ip)
let sviAddresses = (foreach svi in [interface.routedVlan]
                    where isPresent(svi) && isPresent(svi.ipv4)
                    foreach address in svi.ipv4.addresses
                    select address.ip)
let subVrfs = (foreach sub in interface.subinterfaces select distinct sub.networkInstanceName)
let sviVrf = (foreach svi in [interface.routedVlan] where isPresent(svi) select svi.networkInstanceName)
select {
  Device: device.name,
  Interface: interface.name,
  Type: interface.interfaceType,
  Admin: interface.adminStatus,
  Oper: interface.operStatus,
  MTU: interface.mtu,
  "Speed (Mbps)": interface.ethernet.speedMbps,
  MAC: interface.ethernet.macAddress,
  "IPv4 addresses": subAddresses + sviAddresses,
  VRFs: subVrfs + sviVrf,
  Description: interface.description
}
order by Device asc natural, Interface asc natural;
`
	invVLANs = `@query
query(deviceName: String, vlanName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach vlan in device.vlans
where vlanName == "" || vlan.name == vlanName
foreach range in vlan.ranges
select { Device: device.name, VLAN: vlan.name, From: range.from, To: range.to }
order by Device asc natural, From asc;
`
	invVRFs = `@query
query(deviceName: String, vrfName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach instance in device.networkInstances
where vrfName == "" || instance.name == vrfName
select { Device: device.name, VRF: instance.name, Type: instance.instanceType, Interfaces: length(instance.interfaces) }
order by Device asc natural, VRF asc natural;
`
	invHosts = `@query
query(deviceName: String, hostName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach host in device.hosts
where hostName == "" || host.name == hostName
select { Device: device.name, Host: host.name, Type: host.hostType, Addresses: host.addresses, MAC: host.macAddress, Interfaces: host.interfaces }
order by Device asc natural, Host asc natural;
`
	invCloudSubnets = `@query
query(accountName: String, vpcName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
foreach vpc in account.vpcs
where vpcName == "" || vpc.name == vpcName
foreach subnet in vpc.subnets
select {
  Account: account.name,
  VPC: vpc.name,
  Subnet: subnet.name,
  ID: subnet.id,
  Region: subnet.region,
  Zone: subnet.availabilityZone,
  Addresses: subnet.addresses,
  Interfaces: length(subnet.ifaces),
  "Route table": subnet.routeTableId
}
order by Account asc natural, VPC asc natural, Subnet asc natural;
`
	invCloudInstances = `@query
query(accountName: String, vpcName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
foreach vpc in account.vpcs
where vpcName == "" || vpc.name == vpcName
foreach instance in vpc.computeInstances
let addresses = (foreach subnet in vpc.subnets
                 foreach iface in subnet.ifaces
                 where iface.computeInstanceId == instance.id
                 foreach ip in iface.ipAddresses
                 select ip)
select {
  Account: account.name,
  VPC: vpc.name,
  Instance: instance.name,
  ID: instance.id,
  Type: instance.instanceType,
  Image: instance.imageName,
  Up: instance.isUp,
  Interfaces: length(instance.instanceIfaces),
  Addresses: addresses,
  Tags: instance.tags
}
order by Account asc natural, VPC asc natural, Instance asc natural;
`
	// invRoutes is the forwarding table Forward modelled: one row per next hop of every IPv4 route, per device and VRF
	invRoutes = `@query
query(deviceName: String, vrfName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach instance in device.networkInstances
where vrfName == "" || instance.name == vrfName
where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)
foreach entry in instance.afts.ipv4Unicast.ipEntries
foreach hop in entry.nextHops
select {
  Device: device.name,
  VRF: instance.name,
  Prefix: entry.prefix,
  Protocol: hop.originProtocol,
  "Next hop": hop.ipAddress,
  Interface: hop.interfaceName,
  Type: hop.nextHopType
}
order by Device asc natural, VRF asc natural, Prefix asc;
`
	// invDefaultRoutes says, per device and VRF, whether an IPv4 default route is in the table (the one fact a blackhole investigation wants first)
	invDefaultRoutes = `@query
query(deviceName: String, vrfName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach instance in device.networkInstances
where vrfName == "" || instance.name == vrfName
where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)
let defaults = (foreach entry in instance.afts.ipv4Unicast.ipEntries
                where entry.prefix == ipSubnet("0.0.0.0/0")
                select entry)
select {
  Device: device.name,
  VRF: instance.name,
  Routes: length(instance.afts.ipv4Unicast.ipEntries),
  "Default route": length(defaults) > 0
}
order by Device asc natural, VRF asc natural;
`
	// invIGPNeighbors: OSPF adjacencies as Forward modelled them (it models no IS-IS, EIGRP or RIP adjacency)
	invIGPNeighbors = `@query
query(deviceName: String, vrfName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach instance in device.networkInstances
where vrfName == "" || instance.name == vrfName
foreach protocol in instance.protocols
where isPresent(protocol.ospf)
foreach area in protocol.ospf.areas
foreach neighbor in area.neighbors
select {
  Device: device.name,
  VRF: instance.name,
  Protocol: "OSPF",
  Area: area.id,
  Process: area.processId,
  Role: neighbor.role,
  "Remote router ID": neighbor.remoteRouterId,
  "Remote address": neighbor.remoteInterfaceIp,
  "Local interface": neighbor.localInterface,
  Cost: neighbor.cost,
  "Remote device": neighbor.remotePeer?.deviceName
}
order by Device asc natural, VRF asc natural, Area asc;
`
	// invCloudAccounts is one row per cloud account with the collected flag, so a VPC list with no instances can be judged: an account Forward collected that is empty, or an
	// account it never collected.
	invCloudAccounts = `@query
query(accountName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
select {
  Account: account.name,
  ID: account.id,
  Cloud: account.cloudType,
  Collected: account.collected,
  "Cloud setup": account.cloudSetupId,
  VPCs: length(account.vpcs),
  Subnets: sum(foreach vpc in account.vpcs select length(vpc.subnets)),
  Instances: sum(foreach vpc in account.vpcs select length(vpc.computeInstances))
}
order by Account asc natural;
`
	invCloud = `@query
query(accountName: String, vpcName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
foreach vpc in account.vpcs
where vpcName == "" || vpc.name == vpcName
select {
  Account: account.name,
  Cloud: account.cloudType,
  Collected: account.collected,
  VPC: vpc.name,
  ID: vpc.id,
  Regions: vpc.cloudRegions,
  "CIDR blocks": vpc.ipv4CidrBlocks,
  Subnets: length(vpc.subnets),
  Instances: length(vpc.computeInstances)
}
order by Account asc natural, VPC asc natural;
`

	// the cloud model beyond the VPC list: route tables, security group rules, and the gateways and peerings that join a VPC to anything else
	invCloudRoutes = `@query
query(accountName: String, vpcName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
foreach vpc in account.vpcs
foreach table in vpc.routeTables
where vpcName == "" || vpc.name == vpcName || table.name == vpcName
foreach route in table.routes
select {
  Account: account.name,
  VPC: vpc.name,
  "Route table": table.name,
  Region: table.region,
  Prefixes: route.prefixes,
  "Route type": replace(toString(route.routeType), "CloudRouteType.", ""),
  "Next hop": replace(toString(route.nextHop), "CloudRouteNextHop.", ""),
  Priority: route.priority,
  Inactive: route.inactive
}
order by Account asc natural, VPC asc natural, "Route table" asc natural;
`
	// one row per gateway kind and state, over the whole snapshot (or the account and VPC asked for), so "how many VPN connections are down" does not need every row
	invCloudGatewaysSummary = `@query
query(accountName: String, vpcName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
foreach vpc in account.vpcs
where vpcName == "" || vpc.name == vpcName
let vpns = (foreach g in vpc.vpnGateways foreach c in g.vpnConnections select {Kind: "vpn connection", State: if c.isUp then "up" else "down"})
let others = (foreach p in vpc.vpcPeerings select {Kind: "vpc peering", State: "n/a"}) + (foreach g in vpc.inetGateways select {Kind: "internet gateway", State: "n/a"}) + (foreach g in vpc.natGateways select {Kind: "nat gateway", State: "n/a"})
foreach item in vpns + others
group item as items by {Kind: item.Kind, State: item.State} as g
select {Kind: g.Kind, State: g.State, Count: length(items)};`
	invCloudSecurity = `@query
query(accountName: String, vpcName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
foreach vpc in account.vpcs
where vpcName == "" || vpc.name == vpcName
foreach group in vpc.securityGroups
foreach direction in ["ingress", "egress"]
foreach rule in (if direction == "ingress" then group.ingressRules else group.egressRules)
select {
  Account: account.name,
  VPC: vpc.name,
  "Security group": group.name,
  Direction: direction,
  Action: replace(toString(rule.action), "SecurityRuleAction.", ""),
  Source: rule.match.ipv4Src,
  Destination: rule.match.ipv4Dst,
  Protocol: rule.match.ipProtocol,
  "Source ports": rule.match.tpSrc,
  "Destination ports": rule.match.tpDst
}
order by Account asc natural, VPC asc natural, "Security group" asc natural;
`
	invCloudGateways = `@query
query(accountName: String, vpcName: String) =
foreach account in network.cloudAccounts
where accountName == "" || account.name == accountName
foreach vpc in account.vpcs
where vpcName == "" || vpc.name == vpcName
let peerings = (foreach p in vpc.vpcPeerings select {Kind: "vpc peering", Name: p.name, Detail: p.sourceVpcId + " to " + p.destinationVpcId})
let vpns = (foreach g in vpc.vpnGateways foreach c in g.vpnConnections select {Kind: "vpn connection", Name: g.name + " / " + c.name, Detail: if c.isUp then "up" else "down"})
let inets = (foreach g in vpc.inetGateways select {Kind: "internet gateway", Name: g.name, Detail: g.region})
let nats = (foreach g in vpc.natGateways select {Kind: "nat gateway", Name: g.name, Detail: g.region})
foreach item in peerings + vpns + inets + nats
select {Account: account.name, VPC: vpc.name, Kind: item.Kind, Name: item.Name, Detail: item.Detail}
order by Account asc natural, VPC asc natural, Kind asc natural, Name asc natural;
`

	invSummaryCounts = `[{
  Devices: length(network.devices),
  Interfaces: sum(foreach device in network.devices select length(device.interfaces)),
  VLANs: sum(foreach device in network.devices select length(device.vlans)),
  VRFs: sum(foreach device in network.devices select length(device.networkInstances)),
  Hosts: sum(foreach device in network.devices select length(device.hosts)),
  "Cloud accounts": length(network.cloudAccounts)
}]`
	invSummaryVendors = `foreach device in network.devices
group device.name as names by device.platform.vendor as vendor
select { Vendor: vendor, Devices: length(names) }
order by Devices desc, Vendor asc`
	invSummaryTypes = `foreach device in network.devices
group device.name as names by device.platform.deviceType as type
select { Type: type, Devices: length(names) }
order by Devices desc, Type asc`
)

// Rows are the expensive part of a result (about 130 tokens each), so the default is small and the cap is modest; page with offset.
const (
	defaultInventoryLimit = 25
	maxInventoryLimit     = 200
)

// Inputs by kind, for the one kind that has inputs of its own: ip_owner takes ips and nothing that pages or filters rows.
var inventoryKindInputs = map[string][]string{
	"ip_owner": {"ips", "snapshot_id"},
	"rows":     {"device", "account", "name", "limit", "offset", "snapshot_id"},
}

func inspectInventory(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inventoryInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Kind == "ip_owner" {
		if err := rejectForeignInputs(raw, inventoryName, "kind", "ip_owner", inventoryKindInputs); err != nil {
			return result.Result{}, err
		}
		return findIPOwner(ctx, s, findIPOwnerInput{NetworkID: in.NetworkID, IPs: in.IPs, SnapshotID: in.SnapshotID})
	}
	if len(in.IPs) > 0 {
		return result.Result{}, fmt.Errorf("%w: ips is an input of kind ip_owner, not of kind %s", ErrInvalidInput, in.Kind)
	}
	if err := checkKindFilters(in, raw); err != nil {
		return result.Result{}, err
	}
	if in.Limit <= 0 {
		in.Limit = defaultInventoryLimit
	}
	if in.Limit > maxInventoryLimit {
		in.Limit = maxInventoryLimit
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if snap != nil && !fwd.IsReady(snap) {
		return notReadySnapshot(inventoryName, cx, snap, "")
	}
	if !fwd.IsReady(snap) {
		return result.NewUnknown(inventoryName, "No processed snapshot is available to read", cx,
			[]string{"no processed snapshot; nothing was read"}, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	var limits []string
	if cx.State == "predicted" {
		limits = append(limits, "read from a predicted snapshot: these rows describe a prediction, not collected state")
	}
	if in.CompareToSnapshotID != "" {
		if in.Kind != "devices" {
			return result.Result{}, fmt.Errorf("%w: compare_to_snapshot_id is an input of kind devices (which devices were added and removed), not of kind %s", ErrInvalidInput, in.Kind)
		}
		if in.Device != "" || in.Name != "" {
			return result.Result{}, fmt.Errorf("%w: kind devices with compare_to_snapshot_id compares the whole device lists; device and name filter one snapshot's rows", ErrInvalidInput)
		}
		return inventoryCompare(ctx, s, in, cx, limits)
	}
	if in.Kind == "summary" {
		return inventorySummary(ctx, s, in, cx, limits)
	}
	var query string
	params := map[string]any{}
	switch in.Kind {
	case "devices":
		glob, gerr := nameGlobOf(in.Name)
		if gerr != nil {
			return result.Result{}, gerr
		}
		query, params["deviceName"], params["nameGlob"], params["vendorName"] = invDevices, in.Device, glob, vendorParam(in.Vendor)
	case "security_rules_experimental":
		return inventorySecurityRules(ctx, s, in, cx, limits)
	case "interfaces":
		query, params["deviceName"], params["ifaceName"] = invInterfaces, in.Device, in.Name
	case "vlans":
		query, params["deviceName"], params["vlanName"] = invVLANs, in.Device, in.Name
	case "vrfs":
		query, params["deviceName"], params["vrfName"] = invVRFs, in.Device, in.Name
	case "hosts":
		query, params["deviceName"], params["hostName"] = invHosts, in.Device, in.Name
	case "routes", "igp_neighbors":
		query = map[string]string{"routes": invRoutes, "igp_neighbors": invIGPNeighbors}[in.Kind]
		params["deviceName"], params["vrfName"] = in.Device, in.Name
	case "cloud_subnets", "cloud_instances":
		query = map[string]string{"cloud_subnets": invCloudSubnets, "cloud_instances": invCloudInstances}[in.Kind]
		params["accountName"], params["vpcName"] = in.Account, in.Name
	case "cloud_accounts":
		query, params["accountName"] = invCloudAccounts, in.Account
	case "cloud", "cloud_routes", "cloud_security", "cloud_gateways":
		query = map[string]string{"cloud": invCloud, "cloud_routes": invCloudRoutes, "cloud_security": invCloudSecurity, "cloud_gateways": invCloudGateways}[in.Kind]
		params["accountName"], params["vpcName"] = in.Account, in.Name
	default:
		return result.NewError(inventoryName, "unknown kind "+in.Kind, cx), nil
	}
	out, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: query, Parameters: params, SnapshotID: fwd.SnapshotID(cx), Limit: in.Limit, Offset: in.Offset})
	if errors.Is(err, fwd.ErrSnapshotNotReady) {
		return result.NewUnknown(inventoryName, "The snapshot became unavailable; nothing was read", cx, []string{err.Error()}, result.Options{})
	}
	if err != nil {
		return result.Result{}, err
	}
	filters := map[string]any{}
	for k, v := range map[string]string{"device": in.Device, "account": in.Account, "name": in.Name} {
		if v != "" {
			filters[k] = v
		}
	}
	if out.Total == 0 || (len(out.Items) == 0 && in.Offset == 0) {
		reason := "none exist in this snapshot"
		if len(filters) > 0 {
			reason = "none match the filters"
		}
		limits = append(limits, fmt.Sprintf("no %s were returned (%s); the entity may not exist, or a filter may be wrong (names are matched exactly), and this skill cannot tell which", noun(in.Kind), reason))
		switch in.Kind {
		case "igp_neighbors":
			limits = append(limits, "OSPF adjacencies only: Forward's model has no IS-IS, EIGRP or RIP adjacencies, so an empty list does not mean none run (inspect-device-files can read the device's own routing-protocol output)")
		case "cloud_routes", "cloud_security", "cloud_gateways":
			limits = append(limits, cloudFilterHint(ctx, s, in, cx)...)
		}
		switch in.Kind {
		case "cloud_subnets", "cloud_instances", "cloud", "cloud_accounts":
			limits = append(limits, "nothing here proves the cloud account is empty: Collected true does not mean every resource type was read. Check cloud_accounts for Collected, investigate-collection-failure view exceptions for collector errors, and inspect-collection view config for the cloud setup's regions")
		}
		return result.NewUnknown(inventoryName, fmt.Sprintf("No %s were returned", noun(in.Kind)), cx, limits, result.Options{})
	}
	if len(out.Items) == 0 {
		limits = append(limits, fmt.Sprintf("offset %d is past the end of the %d %s", in.Offset, out.Total, noun(in.Kind)))
		return result.NewUnknown(inventoryName, fmt.Sprintf("Offset %d is beyond the %d %s", in.Offset, out.Total, noun(in.Kind)), cx, limits, result.Options{})
	}
	end := in.Offset + len(out.Items)
	var omitted []result.Omission
	if in.Offset > 0 || int64(end) < out.Total {
		omitted = append(omitted, result.Omission{What: noun(in.Kind), Total: int(out.Total), Shown: len(out.Items), From: in.Offset, Paged: true, Next: pageNext(end, int(out.Total))})
	}
	detail := map[string]any{"kind": in.Kind, "filters": filters, "total": out.Total, "offset": in.Offset, "returned": len(out.Items), "rows": fwd.Records(out.Items)}
	finding := fmt.Sprintf("%d %s returned (%d match)", len(out.Items), noun(in.Kind), out.Total)
	next := inventoryNext(in.Kind)
	if in.Kind == "cloud_gateways" {
		// the counts by kind and state cover every gateway that matches the filters, not just this page
		if sum, serr := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: invCloudGatewaysSummary, Parameters: params, SnapshotID: fwd.SnapshotID(cx), Limit: maxInventoryLimit}); serr == nil {
			recs := fwd.Records(sum.Items)
			detail["by_kind_and_state"] = recs
			down := int64(0)
			for _, r := range recs {
				if r["Kind"] == "vpn connection" && r["State"] == "down" {
					if n, ok := num(r["Count"]); ok {
						down += n
					}
				}
			}
			if down > 0 {
				finding += fmt.Sprintf("; %d VPN connection(s) are DOWN (by_kind_and_state counts every gateway that matches, not just this page)", down)
			}
		} else {
			limits = append(limits, "the counts by gateway kind and state could not be read: "+serr.Error())
		}
	}
	if in.Kind == "cloud_accounts" {
		notCollected := 0
		for _, r := range detail["rows"].([]map[string]any) {
			if c, ok := r["Collected"].(bool); ok && !c {
				notCollected++
			}
		}
		if notCollected > 0 {
			finding += fmt.Sprintf("; %d account(s) were NOT collected", notCollected)
			limits = append(limits, "an account with Collected false was not collected in this snapshot: its VPCs and instances are absent or empty because Forward has no data for it, not because the account is empty")
			next = []string{"inspect-collection", "investigate-collection-failure"}
		}
	}
	switch in.Kind {
	case "interfaces":
		limits = append(limits, "IPv4 addresses are those on subinterfaces and on routed-VLAN (SVI) interfaces, with the VRFs they are in; IPv6 addresses and FHRP virtual addresses are not in this list (inspect-inventory kind ip_owner reads the FHRP ones)")
	case "routes":
		limits = append(limits, "the forwarding table as Forward modelled it from the collected state, one row per next hop of every IPv4 route (IPv6 and MPLS tables are not read); filter by device and name (the VRF), page with offset")
		if dr, derr := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: invDefaultRoutes, Parameters: params, SnapshotID: fwd.SnapshotID(cx), Limit: maxInventoryLimit}); derr != nil {
			limits = append(limits, "whether each VRF has a default route could not be read: "+derr.Error())
		} else {
			defs := fwd.Records(dr.Items)
			missing := 0
			for _, d := range defs {
				if v, ok := d["Default route"].(bool); ok && !v {
					missing++
				}
			}
			detail["default_route_by_vrf"] = defs
			if missing > 0 {
				finding += fmt.Sprintf("; %d of %d VRF(s) listed have NO IPv4 default route", missing, len(defs))
			}
			if int64(len(defs)) < dr.Total {
				limits = append(limits, fmt.Sprintf("default_route_by_vrf lists the first %d of %d VRFs; narrow with device or name", len(defs), dr.Total))
			}
		}
	case "igp_neighbors":
		limits = append(limits, "OSPF adjacencies only: Forward's model has no IS-IS, EIGRP or RIP adjacencies, so an empty list does not mean none run; a listed neighbor is a modelled adjacency, and its state (FULL, DOWN) is not in the model (inspect-device-files can read the device's own OSPF output)")
	case "cloud_accounts", "cloud", "cloud_subnets", "cloud_instances":
		limits = append(limits, "Collected true means Forward collected the account, not that every resource type was read: a collector can ignore an error from one cloud API (a quota or permission call, say) and still return the rest. When instances or subnets are fewer than expected, read investigate-collection-failure view exceptions, and inspect-collection view config for the cloud setup's regions")
	}
	return result.Build(inventoryName, result.OK, finding,
		result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: next,
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", fwd.SnapshotIDPtr(snap), detail,
				fmt.Sprintf("%s: %d of %d", in.Kind, len(out.Items), out.Total))}})
}

// noun is what a kind's rows are called in a sentence.
func noun(kind string) string {
	switch kind {
	case "security_rules_experimental":
		return "security rule scopes"
	case "cloud_accounts":
		return "cloud accounts"
	case "cloud_subnets":
		return "cloud subnets"
	case "cloud_instances":
		return "cloud compute instances"
	case "routes":
		return "route next hops"
	case "igp_neighbors":
		return "OSPF neighbors"
	case "cloud":
		return "cloud VPCs"
	case "cloud_routes":
		return "cloud routes"
	case "cloud_security":
		return "cloud security rules"
	case "cloud_gateways":
		return "cloud gateways and peerings"
	}
	return kind
}

func inventoryNext(kind string) []string {
	switch kind {
	case "devices":
		return []string{"inspect-vulnerabilities", "investigate-collection-failure"}
	case "interfaces", "hosts":
		return []string{"investigate-reachability"}
	case "cloud_accounts":
		return []string{"inspect-collection", "investigate-collection-failure"}
	case "routes", "igp_neighbors":
		return []string{"investigate-reachability", "inspect-bgp-neighbors"}
	}
	return []string{"check-network-compliance"}
}

func inventorySummary(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	started := time.Now()
	sid := fwd.SnapshotID(cx)
	run := func(q string, limit int) (fwd.NQEOutcome, error) {
		return s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: q, SnapshotID: sid, Limit: limit})
	}
	counts, err := run(invSummaryCounts, 1)
	if errors.Is(err, fwd.ErrSnapshotNotReady) {
		return result.NewUnknown(inventoryName, "The snapshot became unavailable; nothing was read", cx, []string{err.Error()}, result.Options{})
	}
	if err != nil {
		return result.Result{}, err
	}
	rows := fwd.Records(counts.Items)
	devices := 0
	if len(rows) == 1 {
		if f, ok := rows[0]["Devices"].(float64); ok {
			devices = int(f)
		}
	}
	if len(rows) == 0 || devices == 0 {
		return result.NewUnknown(inventoryName, "The snapshot holds no devices, so there is nothing to summarise", cx,
			append(limits, "zero devices: an empty or unmodeled network, or a collection that produced nothing; this skill cannot tell which"),
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	vendors, err := run(invSummaryVendors, 100)
	if err != nil {
		return result.Result{}, err
	}
	types, err := run(invSummaryTypes, 100)
	if err != nil {
		return result.Result{}, err
	}
	if took := time.Since(started); took > 20*time.Second {
		limits = append(limits, fmt.Sprintf("this took %s: the first query against a large snapshot can take minutes while Forward loads it, and later ones take seconds (measured about one second warm on a 40,000-device snapshot), so ask again before concluding the summary itself is slow", took.Round(time.Second)))
	}
	detail := map[string]any{"kind": "summary", "counts": rows[0], "by_vendor": fwd.Records(vendors.Items), "by_device_type": fwd.Records(types.Items)}
	return result.Build(inventoryName, result.OK, fmt.Sprintf("%d devices across %d vendors", devices, len(vendors.Items)),
		result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-vulnerabilities", "investigate-collection-failure"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, strings.TrimSpace(fmt.Sprintf("%d devices", devices)))}})
}

// kindFilters are the filter inputs each kind of rows takes; a filter a kind does not take is refused, never silently ignored (a no-op filter that still says "123 match" misleads).
var kindFilters = map[string][]string{
	"summary": {}, "security_rules_experimental": {"device", "name", "vendor"}, "devices": {"device", "name", "vendor"}, "interfaces": {"device", "name"}, "vlans": {"device", "name"}, "vrfs": {"device", "name"}, "hosts": {"device", "name"},
	"routes": {"device", "name"}, "igp_neighbors": {"device", "name"},
	"cloud": {"account", "name"}, "cloud_routes": {"account", "name"}, "cloud_security": {"account", "name"}, "cloud_gateways": {"account", "name"},
	"cloud_subnets": {"account", "name"}, "cloud_instances": {"account", "name"}, "cloud_accounts": {"account"},
}

// kindFilterMeaning says what name means for a kind, for the refusal message.
var kindNameMeaning = map[string]string{"devices": "a case-insensitive substring of the device name (device is the exact name)", "routes": "the VRF name", "igp_neighbors": "the VRF name"}

func checkKindFilters(in inventoryInput, raw json.RawMessage) error {
	allowed, ok := kindFilters[in.Kind]
	if !ok {
		return nil // an unknown kind is reported by the caller
	}
	var present map[string]json.RawMessage
	if json.Unmarshal(raw, &present) != nil {
		return nil
	}
	ok2 := map[string]bool{}
	for _, k := range allowed {
		ok2[k] = true
	}
	var bad []string
	for _, k := range []string{"device", "account", "name", "vendor"} {
		if _, there := present[k]; there && !ok2[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: kind %s does not take %s (it takes: %s)", ErrInvalidInput, in.Kind, strings.Join(bad, ", "), strings.Join(allowed, ", "))
	}
	if in.Kind == "devices" {
		if _, err := nameGlobOf(in.Name); err != nil {
			return err
		}
	}
	return nil
}

// nameGlobOf turns a device-name substring into the glob the query matches the lower-cased name against. A glob character in it is refused: name is a substring, not a pattern.
func nameGlobOf(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	if strings.ContainsAny(name, "*?[]\\") {
		return "", fmt.Errorf("%w: name is a plain substring of the device name (case-insensitive), not a pattern: it may not contain * ? [ ] or a backslash", ErrInvalidInput)
	}
	return "*" + strings.ToLower(name) + "*", nil
}

// cloudFilterHint says which cloud account names exist when a cloud kind found nothing for the account filter, and when the value given is a VPC name instead (the account filter
// matches an account NAME exactly; a VPC goes in name). It reads the account list once and is silent when that read fails or no account was given.
func cloudFilterHint(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context) []string {
	if in.Account == "" {
		return nil
	}
	out, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: invCloudAccounts, Parameters: map[string]any{"accountName": ""}, SnapshotID: fwd.SnapshotID(cx), Limit: 50})
	if err != nil {
		return nil
	}
	var names []string
	for _, r := range fwd.Records(out.Items) {
		if n := fmt.Sprint(r["Account"]); n != "" && n != "<nil>" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil
	}
	hint := fmt.Sprintf("account is matched against the cloud account NAME exactly; the accounts in this snapshot are: %s", strings.Join(names, ", "))
	for _, n := range names {
		if n == in.Account {
			return []string{fmt.Sprintf("account %q exists, so the filter that matched nothing is name (a VPC or VNet name, exact) or the account has none of this kind", in.Account)}
		}
	}
	return []string{hint + fmt.Sprintf("; %q is not one of them (a VPC or VNet name goes in name, not account)", in.Account)}
}

// inventorySecurityRules is kind security_rules_experimental: per device and scope, how many rulebases, rules and scope objects Forward's EXPERIMENTAL security rules model holds. The
// model is PAN-OS and FortiOS only, FortiOS is off unless the org property NQE_SECURITY_RULES_FORTIOS is true, and it changes between builds, so the result is marked experimental,
// empty is never read as "no rules", and the fields of a current build fall back to a core set when the org's build lacks them.
func inventorySecurityRules(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	glob, gerr := nameGlobOf(in.Name)
	if gerr != nil {
		return result.Result{}, gerr
	}
	params := map[string]any{"deviceName": in.Device, "nameGlob": glob, "vendorName": vendorParam(in.Vendor)}
	limits = append(limits, "rows are ordered by rule count, then address objects, largest first; name is a substring of the device name and vendor is Forward's vendor name (FORTINET, PALO_ALTO_NETWORKS...)")
	limits = append(limits, "EXPERIMENTAL: Forward's security rules model (device.securityPolicy) is experimental and changes between builds; it covers PAN-OS and FortiOS, FortiOS native rules only when the organization property NQE_SECURITY_RULES_FORTIOS is true (inspect-environment shows it), and PAN-OS native rules sit behind their own flag. Do not treat these counts as a complete policy")
	full := true
	out, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: invSecurityRules, Parameters: params, SnapshotID: fwd.SnapshotID(cx), Limit: in.Limit, Offset: in.Offset})
	if _, _, isQuery := fwd.QueryErrors(err); err != nil && isQuery {
		full = false
		limits = append(limits, "this organization's Forward build lacks some current security-model fields, so dynamic address objects, zone objects and user groups are not counted; `fwdctl nqe lint --org` shows what its schema has")
		out, err = s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: invSecurityRulesCore, Parameters: params, SnapshotID: fwd.SnapshotID(cx), Limit: in.Limit, Offset: in.Offset})
	}
	if err != nil {
		return result.Result{}, err
	}
	if out.Total == 0 || len(out.Items) == 0 {
		return result.NewUnknown(inventoryName, "No device carries the experimental security rules model in this snapshot", cx, append(limits,
			"empty does not mean the devices have no rules: the model is off for FortiOS unless NQE_SECURITY_RULES_FORTIOS is true, PAN-OS native rules have their own flag, and other vendors are not covered; read the device's own config (inspect-device-files) for the rules themselves"),
			result.Options{NextActions: []string{"inspect-environment", "inspect-device-files"}})
	}
	rows := fwd.Records(out.Items)
	var scopes, rules, addr int64
	vendors := map[string]int{}
	for _, r := range rows {
		scopes++
		if n, ok := num(r["Rules"]); ok {
			rules += n
		}
		if n, ok := num(r["Address objects"]); ok {
			addr += n
		}
		vendors[str(r["Vendor"])]++
	}
	var omitted []result.Omission
	if in.Offset > 0 || int64(len(rows)) < out.Total {
		omitted = append(omitted, result.Omission{What: "scopes", Total: int(out.Total), Shown: len(rows), From: in.Offset, Paged: true, Next: joinNext(pageNext(in.Offset+len(rows), int(out.Total)), "the totals cover the rows shown")})
	}
	finding := fmt.Sprintf("EXPERIMENTAL model: %d scope row(s) across %d vendor(s) on this page, %d rules and %d address objects", scopes, len(vendors), rules, addr)
	detail := map[string]any{"kind": in.Kind, "experimental": true, "total": out.Total, "offset": in.Offset, "returned": len(rows), "current_build_fields": full, "by_vendor": vendors, "rows": rows}
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-environment", "inspect-device-files"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, finding)}})
}

// vendorParam is the vendor filter in the form NQE's toString writes an enumeration ("Vendor.FORTINET"); "" is no filter.
func vendorParam(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		return ""
	}
	return "Vendor." + strings.TrimPrefix(v, "VENDOR.")
}
