package fwd

import (
	"context"
	"errors"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Device collection failure types, by what an operator should look at. The vocabulary is Forward's own
// (SnapshotMetrics.deviceCollectionFailures).
var (
	credentialFailures = set("AUTHENTICATION_FAILED", "AUTHORIZATION_FAILED", "PRIV_PASSWORD_ERROR", "KEY_EXCHANGE_FAILED",
		"CONFIG_COLLECTION_UNAUTHORIZED", "AVI_SHELL_AUTH_FAILED", "JUMP_SERVER_PASSWORD_AUTH_FAILED", "JUMP_SERVER_KEY_EXCHANGE_FAILED", "PROXY_SERVER_AUTHENTICATION_FAILED")
	networkFailures = set("CONNECTION_TIMEOUT", "CONNECTION_REFUSED", "NETWORK_UNREACHABLE", "JUMP_SERVER_CONNECTION_TIMEOUT",
		"JUMP_SERVER_CONNECTION_FAILED", "PROXY_SERVER_PING_FAILED", "PROXY_SERVER_PORT_REACHABILITY_FAILED", "PROXY_SERVER_CONNECTION_FAILED")
	sessionFailures = set("IO_ERROR", "SESSION_CLOSED", "STATE_COLLECTION_FAILED", "NO_SPACE_LEFT_ON_COLLECTED_DEVICE", "AVI_CONTROLLER_WITHOUT_HEALTHY_SERVICE_ENGINES")
)

func set(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

// FailureCategory groups a collection failure type: credentials, network_path, device_session or unclassified.
func FailureCategory(t string) string {
	switch {
	case credentialFailures[t]:
		return "credentials"
	case networkFailures[t]:
		return "network_path"
	case sessionFailures[t]:
		return "device_session"
	}
	return "unclassified"
}

// InProgress reports snapshot states whose failure counts are not final.
func InProgress(state string) bool {
	switch state {
	case "UNPACKING", "UNPROCESSED", "PROCESSING", "RESTORING":
		return true
	}
	return false
}

// ProcessFailed reports terminal failure states.
func ProcessFailed(state string) bool {
	switch state {
	case "FAILED", "CANCELED", "TIMED_OUT", "RESTORE_FAILED":
		return true
	}
	return false
}

// NewestSnapshot is the newest non-predicted snapshot in ANY state: a failed collection is the point of
// looking. nil means the network has none.
func (s *Session) NewestSnapshot(ctx context.Context, networkID string) (*forward.Snapshot, error) {
	list, _, err := s.Client.Snapshots.List(ctx, networkID, forward.SnapshotListOptions{})
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(list)), false)
	var keep []forward.Snapshot
	for _, sn := range list {
		if !sn.Predicted() {
			keep = append(keep, sn)
		}
	}
	if len(keep) == 0 {
		return nil, nil
	}
	sort.SliceStable(keep, func(i, j int) bool { return keep[i].CreatedAt > keep[j].CreatedAt })
	return &keep[0], nil
}

// Metrics is a snapshot's collection and processing counts. It is serialised into results as it is, so every field carries its json
// name: an untagged field is written under its Go name, which a garble release build rewrites to an opaque string.
type Metrics struct {
	SuccessfulDevices       int            `json:"successful_devices"`
	FailedDevices           int            `json:"collection_failed_devices"`
	ProcessingFailedDevices int            `json:"processing_failed_devices"`
	CollectionFailures      map[string]int `json:"collection_failures,omitempty"`
	ProcessingFailures      map[string]int `json:"processing_failures,omitempty"`
}

// SnapshotMetrics reads the counts.
func (s *Session) SnapshotMetrics(ctx context.Context, snapshotID string) (Metrics, error) {
	m, _, err := s.Client.Snapshots.Metrics(ctx, snapshotID)
	if err != nil {
		return Metrics{}, err
	}
	return Metrics{SuccessfulDevices: m.NumSuccessfulDevices, FailedDevices: m.NumCollectionFailureDevices,
		ProcessingFailedDevices: m.NumProcessingFailureDevices,
		CollectionFailures:      m.DeviceCollectionFailures, ProcessingFailures: m.DeviceProcessingFailures}, nil
}

// Exception is one processing exception, grouped by Forward.
type Exception struct {
	Type        string   `json:"type"`
	Occurrences int      `json:"occurrences"`
	Devices     []string `json:"devices"`
	// StackTrace is Forward's raw trace for the exception; skills cite only its first line.
	StackTrace string `json:"-"`
}

// SnapshotExceptions returns the processing exceptions, and false when the caller may not read them (they
// need the DEBUG_SNAPSHOTS permission). "Not read" must be reported as such, never as "none".
func (s *Session) SnapshotExceptions(ctx context.Context, snapshotID string) ([]Exception, bool, error) {
	list, _, err := s.Client.Snapshots.Exceptions(ctx, snapshotID)
	var er *forward.ErrorResponse
	if errors.As(err, &er) && er.Response != nil && (er.Response.StatusCode == 403 || er.Response.StatusCode == 401) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := make([]Exception, 0, len(list))
	for _, e := range list {
		out = append(out, Exception{Type: e.ExceptionType, Occurrences: e.Occurrences, Devices: e.Devices, StackTrace: e.StackTrace})
	}
	return out, true, nil
}

// CollectorTask is a collection task's outcome.
type CollectorTask struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Type       string `json:"type,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// Task reads a collector task.
func (s *Session) Task(ctx context.Context, taskID string) (CollectorTask, error) {
	t, _, err := s.Client.CollectorTasks.Get(ctx, taskID)
	if err != nil {
		return CollectorTask{}, err
	}
	return CollectorTask{ID: string(t.ID), Status: t.Status, Type: t.Type, StartedAt: t.StartedAt, FinishedAt: t.FinishedAt}, nil
}

// MissingDevice is a neighbour Forward sees (CDP, LLDP, iBGP, OSPF) but did not model.
type MissingDevice struct {
	Name            string   `json:"name"`
	IPAddresses     []string `json:"ip_addresses,omitempty"`
	Vendor          string   `json:"vendor,omitempty"`
	Type            string   `json:"type,omitempty"`
	DiscoveryMethod string   `json:"discovery_method,omitempty"`
	Neighbors       []string `json:"neighbors,omitempty"`
}

// MissingDevices lists the unmodeled neighbours of a snapshot. Paths through them are incomplete.
func (s *Session) MissingDevices(ctx context.Context, networkID, snapshotID string) ([]MissingDevice, error) {
	list, _, err := s.Client.Devices.Missing(ctx, networkID, snapshotID)
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(list)), false)
	out := make([]MissingDevice, 0, len(list))
	for _, d := range list {
		out = append(out, MissingDevice{Name: d.Name, IPAddresses: d.IPAddresses, Vendor: d.Vendor, Type: d.Type, DiscoveryMethod: d.DiscoveryMethod, Neighbors: d.Neighbors})
	}
	return out, nil
}

// SNMPState is what Forward reports for the network's SNMP collection: how many sources have it enabled, how many of those have a
// credential, and what the last collection did (reported = a status exists; errors by SnmpErrorType).
type SNMPState struct {
	Sources, Enabled, EnabledWithCredential int
	Reported, Succeeded                     int
	Errors                                  map[string]int
}

// SNMPCollection reads the classic sources and their SNMP collection status. Forward sends a status only for a source with SNMP
// collection enabled, so a source with none is "off or never ran", not "succeeded".
func (s *Session) SNMPCollection(ctx context.Context, networkID string) (SNMPState, error) {
	st := SNMPState{Errors: map[string]int{}}
	devs, err := s.ClassicDevices(ctx, networkID)
	if err != nil {
		return st, err
	}
	st.Sources = len(devs)
	for _, d := range devs {
		if d.EnableSNMPCollection != nil && *d.EnableSNMPCollection {
			st.Enabled++
			if d.SNMPCredentialID != "" {
				st.EnabledWithCredential++
			}
		}
	}
	list, _, err := s.Client.ClassicDevices.ListTestStatuses(ctx, networkID)
	if err != nil {
		return st, err
	}
	for _, t := range list {
		if !t.SNMPCollectionReported {
			continue
		}
		st.Reported++
		if t.SNMPCollectionErrorType == "" {
			st.Succeeded++
		} else {
			st.Errors[t.SNMPCollectionErrorType]++
		}
	}
	return st, nil
}

// CollectionMetrics reads each device's collection duration and slowest command (an empty snapshotID means the latest processed).
func (s *Session) CollectionMetrics(ctx context.Context, networkID, snapshotID string) (*forward.SnapshotCollectionMetrics, error) {
	m, _, err := s.Client.Snapshots.CollectionMetrics(ctx, networkID, snapshotID)
	if err == nil && m != nil {
		s.Annotate(intp(len(m.Devices)), false)
	}
	return m, err
}

// SnapshotLog reads up to maxBytes of a snapshot's collection log (one device's when device is set). truncated says there was more.
func (s *Session) SnapshotLog(ctx context.Context, snapshotID, device, level string, maxBytes int64) (text string, truncated bool, err error) {
	var b strings.Builder
	_, truncated, _, err = s.Client.Snapshots.Logs(ctx, snapshotID, forward.SnapshotLogOptions{DeviceName: device, Level: level}, maxBytes, &b)
	return b.String(), truncated, err
}
