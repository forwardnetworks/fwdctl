package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	// IPs belongs to kind ip_owner: the IPv4 addresses whose owning interface to find.
	IPs []string `json:"ips"`
}

// Every filter is a query parameter; "" means no filter. Nothing from the caller is spliced into the query text.
const (
	invDevices = `@query
query(deviceName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
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
	invInterfaces = `@query
query(deviceName: String, ifaceName: String) =
foreach device in network.devices
where deviceName == "" || device.name == deviceName
foreach interface in device.interfaces
where ifaceName == "" || interface.name == ifaceName
let addresses = (foreach sub in interface.subinterfaces
                 foreach address in sub.ipv4.addresses
                 select address.ip)
select {
  Device: device.name,
  Interface: interface.name,
  Type: interface.interfaceType,
  Admin: interface.adminStatus,
  Oper: interface.operStatus,
  MTU: interface.mtu,
  "Speed (Mbps)": interface.ethernet.speedMbps,
  MAC: interface.ethernet.macAddress,
  "IPv4 addresses": addresses,
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
where vpcName == "" || vpc.name == vpcName
foreach table in vpc.routeTables
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
	if !fwd.IsReady(snap) {
		return result.NewUnknown(inventoryName, "No processed snapshot is available to read", cx,
			[]string{"no processed snapshot; nothing was read"}, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	var limits []string
	if cx.State == "predicted" {
		limits = append(limits, "read from a predicted snapshot: these rows describe a prediction, not collected state")
	}
	if in.Kind == "summary" {
		return inventorySummary(ctx, s, in, cx, limits)
	}
	var query string
	params := map[string]any{}
	switch in.Kind {
	case "devices":
		query, params["deviceName"] = invDevices, in.Device
	case "interfaces":
		query, params["deviceName"], params["ifaceName"] = invInterfaces, in.Device, in.Name
	case "vlans":
		query, params["deviceName"], params["vlanName"] = invVLANs, in.Device, in.Name
	case "vrfs":
		query, params["deviceName"], params["vrfName"] = invVRFs, in.Device, in.Name
	case "hosts":
		query, params["deviceName"], params["hostName"] = invHosts, in.Device, in.Name
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
		return result.NewUnknown(inventoryName, fmt.Sprintf("No %s were returned", noun(in.Kind)), cx, limits, result.Options{})
	}
	if len(out.Items) == 0 {
		limits = append(limits, fmt.Sprintf("offset %d is past the end of the %d %s", in.Offset, out.Total, noun(in.Kind)))
		return result.NewUnknown(inventoryName, fmt.Sprintf("Offset %d is beyond the %d %s", in.Offset, out.Total, noun(in.Kind)), cx, limits, result.Options{})
	}
	end := in.Offset + len(out.Items)
	if int64(end) < out.Total {
		limits = append(limits, fmt.Sprintf("%d %s match; rows %d-%d shown. Page with offset=%d.", out.Total, noun(in.Kind), in.Offset+1, end, end))
	}
	detail := map[string]any{"kind": in.Kind, "filters": filters, "total": out.Total, "offset": in.Offset, "returned": len(out.Items), "rows": fwd.Records(out.Items)}
	finding := fmt.Sprintf("%d %s returned (%d match)", len(out.Items), noun(in.Kind), out.Total)
	next := inventoryNext(in.Kind)
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
	return result.Build(inventoryName, result.OK, finding,
		result.Deterministic, cx, result.Options{Limits: limits, NextActions: next,
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", fwd.SnapshotIDPtr(snap), detail,
				fmt.Sprintf("%s: %d of %d", in.Kind, len(out.Items), out.Total))}})
}

// noun is what a kind's rows are called in a sentence.
func noun(kind string) string {
	switch kind {
	case "cloud_accounts":
		return "cloud accounts"
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
	}
	return []string{"check-network-compliance"}
}

func inventorySummary(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
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
	detail := map[string]any{"kind": "summary", "counts": rows[0], "by_vendor": fwd.Records(vendors.Items), "by_device_type": fwd.Records(types.Items)}
	return result.Build(inventoryName, result.OK, fmt.Sprintf("%d devices across %d vendors", devices, len(vendors.Items)),
		result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-vulnerabilities", "investigate-collection-failure"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, strings.TrimSpace(fmt.Sprintf("%d devices", devices)))}})
}
