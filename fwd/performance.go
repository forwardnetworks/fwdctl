package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Metric queries. Forward's enums are exact strings: device metrics CPU and MEMORY; interface metrics UTILIZATION, PACKET_LOSS
// and ERROR, each INGRESS or EGRESS. Zero samples means no performance data, not a healthy network.

// DeviceMetrics reads the latest device metric of one type.
func (s *Session) DeviceMetrics(ctx context.Context, networkID string, q forward.MetricQuery) ([]forward.DeviceMetric, error) {
	m, _, err := s.Client.Performance.DeviceMetrics(ctx, networkID, q)
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(m.Metrics)), false)
	return m.Metrics, nil
}

// InterfaceMetrics reads the latest interface metric of one type and direction.
func (s *Session) InterfaceMetrics(ctx context.Context, networkID string, q forward.MetricQuery) ([]forward.InterfaceMetric, error) {
	m, _, err := s.Client.Performance.InterfaceMetrics(ctx, networkID, q)
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(m.Metrics)), false)
	return m.Metrics, nil
}

// UnhealthyDevices returns Forward's own unhealthy-device document.
func (s *Session) UnhealthyDevices(ctx context.Context, networkID string, q forward.MetricQuery) ([]byte, error) {
	d, _, err := s.Client.Performance.UnhealthyDevices(ctx, networkID, q)
	return d, err
}

// UnhealthyInterfaces returns Forward's own unhealthy-interface document for the given devices.
func (s *Session) UnhealthyInterfaces(ctx context.Context, networkID string, q forward.MetricQuery, devices []string) ([]byte, error) {
	d, _, err := s.Client.Performance.UnhealthyInterfaces(ctx, networkID, q, forward.UnhealthyInterfacesRequest{Devices: devices})
	return d, err
}

// DeviceHistory returns the metric history of the named devices.
func (s *Session) DeviceHistory(ctx context.Context, networkID string, q forward.MetricQuery, devices []string) ([]forward.DeviceMetricHistory, error) {
	h, _, err := s.Client.Performance.DeviceMetricHistory(ctx, networkID, q, forward.DeviceMetricHistoryRequest{Devices: devices})
	if err != nil {
		return nil, err
	}
	return h.Metrics, nil
}

// InterfaceHistory returns the metric history of interfaces, in the query's device/interface filter.
func (s *Session) InterfaceHistory(ctx context.Context, networkID string, q forward.MetricQuery, ifaces []forward.InterfaceWithDirection) ([]forward.InterfaceMetricHistory, error) {
	h, _, err := s.Client.Performance.InterfaceMetricHistory(ctx, networkID, q, forward.InterfaceMetricHistoryRequest{Interfaces: ifaces})
	if err != nil {
		return nil, err
	}
	return h.Metrics, nil
}
