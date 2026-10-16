package skills

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// maxDiscoveredAdds is how many discovered devices one add_discovered call adds.
const maxDiscoveredAdds = 50

// planDiscovery manages Forward's device discovery. start scans the network FROM A COLLECTOR (a subnet scan or a seeded or direct discovery), so it is a dry run that says so and applies only with confirm equal to the
// discovery type; stop ends the running discovery (confirm "stop"); add_discovered adds named discovered devices as classic devices, each pinned to the collector of the job that found it (confirm equal to the count).
func planDiscovery(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	if in.Action == "schedule_create" || in.Action == "schedule_delete" {
		return planDiscoverySchedule(ctx, s, in)
	}
	found, _, err := s.Client.DeviceDiscovery.ListDiscovered(ctx, in.NetworkID, time.Time{})
	if err != nil {
		return nil, err
	}
	var running []map[string]any
	for _, j := range found.Jobs {
		if j.EndedAt == "" {
			running = append(running, map[string]any{"id": j.ID, "type": j.Type, "collector_id": string(j.CollectorID), "started_at": j.StartedAt})
		}
	}
	switch in.Action {
	case "start":
		var def discoveryDef
		if err := decodeDefinition(in.Definition, &def, "type (SUBNET_SCAN, SEEDED, DIRECT or CLOUD), collector_id; SUBNET_SCAN: subnets, hosts, subnets_to_skip, hostnames_to_skip, jump_server_ids, try_direct_connection, protocols, connection_scan_ports; SEEDED: seed_devices, snmp_credential_id, jump_server_id, recursive, allowed_subnets, subnets_to_skip, hosts_to_skip; CLOUD: setup_ids"); err != nil {
			return nil, err
		}
		params, targets, perr := def.params(true)
		if perr != nil {
			return nil, perr
		}
		if len(running) > 0 {
			return nil, fmt.Errorf("%w: a discovery is already running (job %v, started %v); Forward refuses a second: stop it first (action stop) or wait", ErrInvalidInput, running[0]["id"], running[0]["started_at"])
		}
		rate := forward.DefaultMaxScanConnectionsPerSecond
		if org, _, oerr := s.Client.Collectors.GetOrganizationSettings(ctx); oerr == nil && org.MaxScanConnectionsPerSecond != nil {
			rate = *org.MaxScanConnectionsPerSecond
		}
		collector := def.CollectorID
		if collector == "" {
			collector = "the network's default collector"
		}
		typ := strings.ToUpper(def.Type)
		return &networkPlan{target: fmt.Sprintf("start a %s discovery of %s from %s on network %s", typ, targets, collector, in.NetworkID), action: "start_discovery", after: params, reversible: true, confirm: typ,
			undo: "stop it (action stop); what it already found stays listed (inspect-collection view discovery)",
			limits: []string{
				"this scans: the collector probes the named addresses (connections, and logins for the types that log in) until the job ends or is stopped, so it touches the customer network. The organization's scan rate limit is " + strconv.Itoa(rate) + " connections per second (maxScanConnectionsPerSecond)",
				"Forward answers 409 if a discovery is already running, and refuses a type that logs in to devices when the organization's exploratory-login setting is off; start returns once the job is queued: read the outcome with inspect-collection view discovery",
				"needs MANAGE_COLLECTION_SOURCES; apply needs confirm equal to the discovery type (" + typ + ")",
			},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.DeviceDiscovery.Start(ctx, in.NetworkID, def.CollectorID, params)
				return err
			}}, nil
	case "stop":
		if len(running) == 0 {
			return &networkPlan{target: "stop the running discovery", noop: "no discovery is running", limits: []string{"nothing is running on network " + in.NetworkID}}, nil
		}
		return &networkPlan{target: fmt.Sprintf("stop the running discovery of network %s", in.NetworkID), action: "stop_discovery", before: map[string]any{"running": running}, reversible: false, confirm: "stop",
			undo:   "none: start a new discovery (action start); what the stopped one found stays listed",
			limits: []string{"apply needs confirm \"stop\"; what the job already found stays listed"},
			do: func(ctx context.Context) error {
				_, err := s.Client.DeviceDiscovery.Stop(ctx, in.NetworkID)
				return err
			}}, nil
	case "add_discovered":
		var def struct {
			Names []string `json:"names"`
		}
		if err := decodeDefinition(in.Definition, &def, "names [the discovered devices to add, at most 50]"); err != nil {
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
		if len(names) == 0 || len(names) > maxDiscoveredAdds {
			return nil, fmt.Errorf("%w: add_discovered takes 1 to %d names", ErrInvalidInput, maxDiscoveredAdds)
		}
		classic, _, cerr := s.Client.ClassicDevices.List(ctx, in.NetworkID)
		if cerr != nil {
			return nil, cerr
		}
		have := map[string]bool{}
		for _, d := range classic {
			have[strings.ToLower(d.Name)] = true
		}
		byName := map[string]forward.DiscoveredDevice{}
		for _, d := range found.Devices {
			byName[d.Name] = d
		}
		var reqs []forward.ClassicDeviceRequest
		var rows []map[string]any
		var problems []string
		for _, n := range names {
			d, ok := byName[n]
			switch {
			case !ok:
				problems = append(problems, n+" was not discovered")
				continue
			case have[strings.ToLower(n)]:
				problems = append(problems, n+" is already a collection device")
				continue
			case d.Host == "":
				problems = append(problems, n+" has no host")
				continue
			case d.Type == "" || d.Type == "unknown":
				problems = append(problems, n+" has no type Forward could infer")
				continue
			}
			collector := ""
			if j := found.JobFor(d); j != nil {
				collector = string(j.CollectorID)
			}
			reqs = append(reqs, forward.ClassicDeviceRequest{Name: d.Name, Host: d.Host, Type: d.Type, Port: d.Port, CLICredentialID: d.CLICredentialID, HTTPCredentialID: d.HTTPCredentialID,
				SNMPCredentialID: d.SNMPCredentialID, JumpServerID: d.JumpServerID, CollectorID: collector})
			rows = append(rows, map[string]any{"name": d.Name, "host": d.Host, "type": d.Type, "collector": collector, "cli_credential": d.CLICredentialID, "snmp_credential": d.SNMPCredentialID, "jump_server": d.JumpServerID})
		}
		if len(problems) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrInvalidInput, strings.Join(problems, "; "))
		}
		return &networkPlan{target: fmt.Sprintf("add %d discovered device(s) to the collection list of network %s, each pinned to the collector that discovered it", len(reqs), in.NetworkID), action: "add_discovered_devices",
			after: map[string]any{"devices": rows}, reversible: true, confirm: strconv.Itoa(len(reqs)),
			undo: "delete each device (edit-source object classic_device action delete)",
			limits: []string{
				"each device is added with the type, host, credentials and jump server discovery recorded and pinned to the collector of the job that found it (the collector that could reach it); a device with no credential is added without one and will fail collection until it has one",
				"adding makes the devices collected at the next collection; this does not test them (edit-source classic_device action test does)",
				fmt.Sprintf("at most %d per call; apply needs confirm equal to the number of devices (%d)", maxDiscoveredAdds, len(reqs)),
			},
			do: func(ctx context.Context) error {
				for _, r := range reqs {
					if _, _, err := s.Client.ClassicDevices.Create(ctx, in.NetworkID, r); err != nil {
						return fmt.Errorf("adding %s: %w", r.Name, err)
					}
				}
				return nil
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.ClassicDevices.List(ctx, in.NetworkID)
				got := map[string]bool{}
				for _, d := range l {
					got[d.Name] = true
				}
				for _, r := range reqs {
					if !got[r.Name] {
						return false, nil, e
					}
				}
				return true, nil, e
			}}, nil
	}
	return nil, fmt.Errorf("%w: discovery takes action start, stop or add_discovered", ErrInvalidInput)
}

// discoveryDef is the definition of a discovery start or of the params of a scheduled one.
type discoveryDef struct {
	Type                string   `json:"type"`
	CollectorID         string   `json:"collector_id"`
	Subnets             []string `json:"subnets"`
	Hosts               []string `json:"hosts"`
	SubnetsToSkip       []string `json:"subnets_to_skip"`
	HostnamesToSkip     []string `json:"hostnames_to_skip"`
	JumpServerIDs       []string `json:"jump_server_ids"`
	TryDirectConnection bool     `json:"try_direct_connection"`
	Protocols           []string `json:"protocols"`
	ConnectionScanPorts []int    `json:"connection_scan_ports"`
	SeedDevices         []string `json:"seed_devices"`
	SNMPCredentialID    string   `json:"snmp_credential_id"`
	JumpServerID        string   `json:"jump_server_id"`
	Recursive           bool     `json:"recursive"`
	AllowedSubnets      []string `json:"allowed_subnets"`
	HostsToSkip         []string `json:"hosts_to_skip"`
	SetupIDs            []string `json:"setup_ids"`
}

// params builds the SDK's discovery params and a phrase for what is scanned; a CLOUD discovery is allowed only when allowCloud (it cannot be scheduled).
func (def discoveryDef) params(allowCloud bool) (forward.DiscoveryParams, string, error) {
	switch strings.ToUpper(def.Type) {
	case "SUBNET_SCAN":
		p := forward.SubnetScanParams{Subnets: def.Subnets, Hosts: def.Hosts, SubnetsToSkip: def.SubnetsToSkip, HostnamesToSkip: def.HostnamesToSkip, JumpServerIDs: def.JumpServerIDs,
			TryDirectConnection: def.TryDirectConnection, Protocols: def.Protocols, ConnectionScanPorts: def.ConnectionScanPorts}
		if len(p.Subnets) == 0 && len(p.Hosts) == 0 {
			return nil, "", fmt.Errorf("%w: a SUBNET_SCAN needs subnets or hosts", ErrInvalidInput)
		}
		return p, fmt.Sprintf("%d subnet(s) and %d host(s)", len(p.Subnets), len(p.Hosts)), nil
	case "SEEDED":
		p := forward.SeededDiscoveryParams{SeedDevices: def.SeedDevices, SNMPCredentialID: def.SNMPCredentialID, JumpServerID: def.JumpServerID, Recursive: def.Recursive,
			AllowedSubnets: def.AllowedSubnets, SubnetsToSkip: def.SubnetsToSkip, HostsToSkip: def.HostsToSkip}
		if len(p.SeedDevices) == 0 {
			return nil, "", fmt.Errorf("%w: a SEEDED discovery needs seed_devices (IP addresses)", ErrInvalidInput)
		}
		return p, fmt.Sprintf("%d seed device(s)%s", len(p.SeedDevices), map[bool]string{true: ", recursively", false: ""}[p.Recursive]), nil
	case "DIRECT":
		return forward.DirectDiscoveryParams{}, "the neighbours of the network's own devices", nil
	case "CLOUD":
		if !allowCloud {
			return nil, "", fmt.Errorf("%w: a CLOUD discovery cannot be scheduled (Forward does not schedule it)", ErrInvalidInput)
		}
		p := forward.CloudDiscoveryParams{SetupIDs: def.SetupIDs}
		if len(p.SetupIDs) == 0 {
			return nil, "", fmt.Errorf("%w: a CLOUD discovery needs setup_ids (the cloud setup names)", ErrInvalidInput)
		}
		return p, fmt.Sprintf("%d cloud setup(s)", len(p.SetupIDs)), nil
	}
	return nil, "", fmt.Errorf("%w: type is SUBNET_SCAN, SEEDED, DIRECT or CLOUD", ErrInvalidInput)
}

// planDiscoverySchedule creates or deletes a scheduled discovery. An enabled schedule scans the network again on its schedule (or after every collection when it has none) from the network's default collector.
func planDiscoverySchedule(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	switch in.Action {
	case "schedule_create":
		var def struct {
			discoveryDef
			Name     string `json:"name"`
			Enabled  bool   `json:"enabled"`
			Schedule *struct {
				TimeZone       string   `json:"time_zone"`
				DaysOfTheWeek  []int    `json:"days_of_the_week"`
				Times          []string `json:"times"`
				DayOfTheWeek   *int     `json:"day_of_the_week"`
				WeekOfTheMonth *int     `json:"week_of_the_month"`
				Time           string   `json:"time"`
			} `json:"schedule"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, enabled, type (SUBNET_SCAN, SEEDED or DIRECT) and its params (as for start), schedule {time_zone, days_of_the_week [0=Sun..6] + times [HH:mm]  OR  day_of_the_week + week_of_the_month [1-4] + time}; no schedule = after every collection"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" {
			return nil, fmt.Errorf("%w: schedule_create needs a name", ErrInvalidInput)
		}
		params, targets, perr := def.discoveryDef.params(false)
		if perr != nil {
			return nil, perr
		}
		d := forward.ScheduledDiscoveryDefinition{Enabled: def.Enabled, Name: def.Name, Params: params}
		when := "after every collection"
		if def.Schedule != nil {
			d.Schedule = &forward.DiscoverySchedule{TimeZone: def.Schedule.TimeZone, DaysOfTheWeek: def.Schedule.DaysOfTheWeek, Times: def.Schedule.Times,
				DayOfTheWeek: def.Schedule.DayOfTheWeek, WeekOfTheMonth: def.Schedule.WeekOfTheMonth, Time: def.Schedule.Time}
			when = "on its schedule"
		}
		state := "disabled (it scans nothing until enabled)"
		if def.Enabled {
			state = "enabled"
		}
		return &networkPlan{target: fmt.Sprintf("create scheduled discovery %q (%s, %s of %s) on network %s", def.Name, def.Type, state, targets, in.NetworkID), action: "create_scheduled_discovery", after: d, reversible: true, confirm: def.Name,
			undo: "delete it (action schedule_delete, name its id)",
			limits: []string{
				"an enabled scheduled discovery scans the network from the network's default collector " + when + ", repeatedly, until it is deleted or disabled: it touches the customer network every time",
				"Forward does not send skip lists back on a read, so a stored schedule always shows them empty; apply needs confirm equal to the schedule name",
			},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.DeviceDiscovery.CreateScheduled(ctx, in.NetworkID, d)
				return err
			}}, nil
	case "schedule_delete":
		l, _, err := s.Client.DeviceDiscovery.ListScheduled(ctx, in.NetworkID)
		if err != nil {
			return nil, err
		}
		for _, x := range l {
			if x.ID == in.Name || strings.EqualFold(x.Name, in.Name) {
				before, _ := fwd.Generic(x)
				id := x.ID
				return &networkPlan{target: fmt.Sprintf("delete scheduled discovery %q (%s) from network %s", x.Name, x.ID, in.NetworkID), action: "delete_scheduled_discovery", before: before, reversible: true, confirm: id,
					undo:   "create it again from the before values (action schedule_create)",
					limits: []string{"its discovered devices stay listed; apply needs confirm equal to the schedule id"},
					do: func(ctx context.Context) error {
						_, err := s.Client.DeviceDiscovery.DeleteScheduled(ctx, in.NetworkID, id)
						return err
					}}, nil
			}
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: not a schedule action", ErrInvalidInput)
}
