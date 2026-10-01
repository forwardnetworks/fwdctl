package fwd

import (
	"context"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Version reads the Forward build.
func (s *Session) Version(ctx context.Context) (*forward.APIVersion, error) {
	v, _, err := s.Client.Version.Get(ctx)
	return v, err
}

// CapabilityStatuses is what the SDK has learned about the build. Without a build profile these are "unknown" until a call
// succeeds, so unknown is not unsupported.
func (s *Session) CapabilityStatuses() []forward.CapabilityStatus { return s.Client.Capabilities.All() }

// Organization reads the login's organization.
func (s *Session) Organization(ctx context.Context) (*forward.Organization, error) {
	o, _, err := s.Client.Organizations.Current(ctx)
	return o, err
}

// CurrentUser reads the login.
func (s *Session) CurrentUser(ctx context.Context) (*forward.User, error) {
	u, _, err := s.Client.Users.Current(ctx)
	return u, err
}

// NonDefaultProperties reads the organization properties that differ from Forward's defaults, sorted by name.
func (s *Session) NonDefaultProperties(ctx context.Context) (map[string]string, []string, error) {
	p, _, err := s.Client.Properties.Current(ctx, forward.PropertyFilterNondefault)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]string, len(p))
	names := make([]string, 0, len(p))
	for k, v := range p {
		out[string(k)] = forward.PropertyValueString(v)
		names = append(names, string(k))
	}
	sort.Strings(names)
	return out, names, nil
}

// CVEIndex reads when the vulnerability index was built and last updated.
func (s *Session) CVEIndex(ctx context.Context) (*forward.CVEIndexMetadata, error) {
	m, _, err := s.Client.CVEIndex.Metadata(ctx)
	return m, err
}

// InternetNode reads the network's internet node. nil with no error means none is modelled.
func (s *Session) InternetNode(ctx context.Context, networkID string) (*forward.SyntheticNode, error) {
	n, _, err := s.Client.SyntheticNodes.GetInternetNode(ctx, networkID)
	if NotFound(err) {
		return nil, nil
	}
	return n, err
}

// SyntheticNodes lists the network's intranet nodes or L3 VPNs.
func (s *Session) SyntheticNodes(ctx context.Context, networkID string, kind forward.SyntheticNodeKind) ([]forward.SyntheticNode, error) {
	n, _, err := s.Client.SyntheticNodes.List(ctx, networkID, kind)
	if err == nil {
		s.Annotate(intp(len(n)), false)
	}
	return n, err
}

// LinkOverrides reads the manual topology links (present) and suppressed links (absent) of a snapshot.
func (s *Session) LinkOverrides(ctx context.Context, snapshotID string) (*forward.TopologyOverrides, error) {
	o, _, err := s.Client.Topology.Overrides(ctx, snapshotID)
	return o, err
}

// AllChecks lists a snapshot's checks with the given statuses (all when empty).
func (s *Session) AllChecks(ctx context.Context, snapshotID string, statuses []string) ([]forward.Check, error) {
	list, _, err := s.Client.Checks.List(ctx, snapshotID, forward.CheckListOptions{Statuses: statuses})
	if err == nil {
		s.Annotate(intp(len(list)), false)
	}
	return list, err
}

// CheckDetail reads one check with its diagnosis.
func (s *Session) CheckDetail(ctx context.Context, snapshotID, checkID string) (*forward.CheckDetail, error) {
	d, _, err := s.Client.Checks.Get(ctx, snapshotID, checkID)
	return d, err
}

// PredefinedChecks lists Forward's built-in check catalogue.
func (s *Session) PredefinedChecks(ctx context.Context) ([]forward.PredefinedCheck, error) {
	c, _, err := s.Client.Checks.ListPredefined(ctx)
	if err == nil {
		s.Annotate(intp(len(c)), false)
	}
	return c, err
}

// ClassicDevices lists the devices Forward is told to collect. Credentials are ids here, never secrets.
func (s *Session) ClassicDevices(ctx context.Context, networkID string) ([]forward.ClassicDevice, error) {
	d, _, err := s.Client.ClassicDevices.List(ctx, networkID)
	if err == nil {
		s.Annotate(intp(len(d)), false)
	}
	return d, err
}

// Endpoints lists the collection endpoints (cloud, controllers).
func (s *Session) Endpoints(ctx context.Context, networkID string) ([]forward.Endpoint, error) {
	e, _, err := s.Client.Endpoints.List(ctx, networkID)
	return e, err
}

// JumpServers lists the jump servers.
func (s *Session) JumpServers(ctx context.Context, networkID string) ([]forward.JumpServer, error) {
	j, _, err := s.Client.JumpServers.List(ctx, networkID)
	return j, err
}

// Proxies lists the collection proxies.
func (s *Session) Proxies(ctx context.Context, networkID string) ([]forward.ProxyServer, error) {
	p, _, err := s.Client.Proxies.List(ctx, networkID)
	return p, err
}

// CreateCheck adds a check to a snapshot and returns it as Forward evaluated it. persistent makes later snapshots inherit it.
func (s *Session) CreateCheck(ctx context.Context, snapshotID string, c forward.NewCheck, persistent bool) (*forward.CheckDetail, error) {
	d, _, err := s.Client.Checks.Create(ctx, snapshotID, c, &persistent)
	return d, err
}

// DeactivateCheck stops one check evaluating. It is the only deactivation the skills use: never the whole snapshot.
func (s *Session) DeactivateCheck(ctx context.Context, snapshotID, checkID string) error {
	_, err := s.Client.Checks.Deactivate(ctx, snapshotID, checkID)
	return err
}

// SyntheticNode reads one synthetic node of any kind (nil when absent), including its NQE query and what the query generated.
func (s *Session) SyntheticNode(ctx context.Context, networkID string, kind forward.SyntheticNodeKind, name string) (*forward.SyntheticNode, error) {
	n, _, err := s.Client.SyntheticNodes.Get(ctx, networkID, kind, name)
	return n, err
}

// ComputeSyntheticQuery runs a query as if it were attached to a node of the kind, changing nothing, and returns the connections it would
// generate or the reason it cannot.
func (s *Session) ComputeSyntheticQuery(ctx context.Context, networkID string, kind forward.SyntheticNodeKind, queryID string) (*forward.SyntheticQueryResult, error) {
	r, _, err := s.Client.SyntheticNodes.ComputeQuery(ctx, networkID, kind, queryID)
	return r, err
}

// CompatibleSyntheticQueries lists the saved queries whose rows fit a node kind.
func (s *Session) CompatibleSyntheticQueries(ctx context.Context, networkID string, kind forward.SyntheticNodeKind) ([]forward.SyntheticDeviceQuery, error) {
	q, _, err := s.Client.SyntheticNodes.CompatibleQueries(ctx, networkID, kind)
	return q, err
}

// SetSyntheticQuery attaches a query to a node (queryID "" detaches it) and returns the node as Forward now holds it.
func (s *Session) SetSyntheticQuery(ctx context.Context, networkID string, kind forward.SyntheticNodeKind, name, queryID string) (*forward.SyntheticNode, error) {
	n, _, err := s.Client.SyntheticNodes.SetQuery(ctx, networkID, kind, name, queryID)
	return n, err
}

// InternetSuggestions are the connections Forward suggests for the internet node, from the latest processed snapshot.
func (s *Session) InternetSuggestions(ctx context.Context, networkID string) ([]forward.InternetConnectionSuggestion, error) {
	r, _, err := s.Client.SyntheticNodes.InternetConnectionSuggestions(ctx, networkID)
	return r, err
}

// WanCircuits lists the network's WAN circuits; WanCircuit reads one (nil when absent).
func (s *Session) WanCircuits(ctx context.Context, networkID string) ([]forward.WanCircuit, error) {
	l, _, err := s.Client.WanCircuits.List(ctx, networkID)
	return l, err
}

func (s *Session) WanCircuit(ctx context.Context, networkID, name string) (*forward.WanCircuit, error) {
	c, _, err := s.Client.WanCircuits.Get(ctx, networkID, name)
	return c, err
}

// PutWanCircuit creates or replaces one circuit; DeleteWanCircuit removes one (an absent circuit is not an error).
func (s *Session) PutWanCircuit(ctx context.Context, networkID string, c forward.WanCircuit) error {
	_, err := s.Client.WanCircuits.Put(ctx, networkID, c.Name, c)
	return err
}

func (s *Session) DeleteWanCircuit(ctx context.Context, networkID, name string) error {
	_, err := s.Client.WanCircuits.Delete(ctx, networkID, name)
	return err
}

// BackdateSynthetic applies the CURRENT configuration of a synthetic device kind from an existing snapshot onward and invalidates that snapshot and
// every later one, so they reprocess. kind "wan-circuit" is the WAN circuits; others are the SDK's synthetic node kinds.
func (s *Session) BackdateSynthetic(ctx context.Context, networkID string, kind string, snapshotID string) error {
	if kind == "wan-circuit" {
		_, err := s.Client.WanCircuits.Backdate(ctx, networkID, snapshotID)
		return err
	}
	_, err := s.Client.SyntheticNodes.Backdate(ctx, networkID, forward.SyntheticNodeKind(kind), snapshotID)
	return err
}

// SetInternetExcludedSubnets REPLACES the internet node's list of public subnets that must not be located at it, and returns the node as Forward now
// holds it. An empty list clears it; the SDK refuses a nil one.
func (s *Session) SetInternetExcludedSubnets(ctx context.Context, networkID string, subnets []string) (*forward.SyntheticNode, error) {
	n, _, err := s.Client.SyntheticNodes.SetInternetExcludedSubnets(ctx, networkID, subnets)
	return n, err
}
