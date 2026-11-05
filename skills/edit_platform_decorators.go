package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// planDecorator creates or changes an NQE decorator, or enables or disables one (Forward's own fwd-... decorators can only be enabled or
// disabled). A decorator's query and columns are checked offline before anything is sent, since Forward saves a bad decorator without
// complaint and reports it only when it decorates. Delete is not offered here.
func planDecorator(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	var def struct {
		Name              string   `json:"name"`
		Type              string   `json:"type"`
		QueryID           string   `json:"queryId"`
		Description       string   `json:"description"`
		MetadataColNames  []string `json:"metadataColNames"`
		Enabled           *bool    `json:"enabled"`
		DeviceColName     string   `json:"deviceColName"`
		InterfaceColName  string   `json:"interfaceColName"`
		IPAddressColName  string   `json:"ipAddressColName"`
		HostColName       string   `json:"hostColName"`
		MACAddressColName string   `json:"macAddressColName"`
		HopIndexColName   string   `json:"hopIndexColName"`
		DirectionColName  string   `json:"directionColName"`
	}
	if in.Action == "create" || in.Action == "update" {
		if err := decodeDefinition(in.Definition, &def, "name, type (DEVICE, INTERFACE, HOST, PATH, PATH_DEVICE, PATH_INTERFACE), queryId (a saved library query), metadataColNames (the columns shown; omit for all), deviceColName, interfaceColName, ipAddressColName, hostColName, macAddressColName, hopIndexColName, directionColName, description, enabled"); err != nil {
			return nil, err
		}
	}
	name := strings.TrimSpace(firstNonEmpty(in.Name, def.Name))
	if name == "" {
		return nil, fmt.Errorf("%w: name (the decorator) is required", ErrInvalidInput)
	}
	cur, _, err := s.Client.Decorators.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	verify := func(want func(*forward.Decorator) bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			d, _, err := s.Client.Decorators.Get(ctx, name)
			if err != nil || d == nil {
				return false, nil, err
			}
			g, _ := fwd.Generic(*d)
			return want(d), g, nil
		}
	}
	checkCols := func(typ forward.DecoratorEntityType, queryID string, meta []string, device, iface, ip, host, mac string) ([]string, error) {
		problems, note := checkDecoratorQuery(ctx, s, typ, queryID, decoratorColumns(typ, device, iface, ip, host, mac, meta))
		if len(problems) > 0 {
			return nil, fmt.Errorf("%w: the decorator would be saved but Forward would report it INVALID_DECORATOR or INVALID_QUERY on every entity: %s", ErrInvalidInput, strings.Join(problems, "; "))
		}
		if note != "" {
			return []string{note}, nil
		}
		return []string{"the decorator's columns were checked offline against its query (head commit): the type check is gradual; inspect-platform area decorators reads it back"}, nil
	}
	switch in.Action {
	case "create":
		if cur != nil {
			return nil, fmt.Errorf("%w: a decorator named %q already exists: use update", ErrInvalidInput, name)
		}
		if systemManagedDecorator(name) {
			return nil, fmt.Errorf("%w: names starting fwd- are Forward's own decorators", ErrInvalidInput)
		}
		typ := forward.DecoratorEntityType(strings.ToUpper(strings.TrimSpace(def.Type)))
		switch typ {
		case forward.DecoratorEntityDevice, forward.DecoratorEntityInterface, forward.DecoratorEntityHost, forward.DecoratorEntityPath, forward.DecoratorEntityPathDevice, forward.DecoratorEntityPathInterface:
		default:
			return nil, fmt.Errorf("%w: type is DEVICE, INTERFACE, HOST, PATH, PATH_DEVICE or PATH_INTERFACE", ErrInvalidInput)
		}
		need := map[forward.DecoratorEntityType][]string{forward.DecoratorEntityDevice: {def.DeviceColName}, forward.DecoratorEntityInterface: {def.DeviceColName, def.InterfaceColName}, forward.DecoratorEntityHost: {def.IPAddressColName}}[typ]
		for _, n := range need {
			if n == "" {
				return nil, fmt.Errorf("%w: a %s decorator needs its join columns (DEVICE: deviceColName; INTERFACE: deviceColName and interfaceColName; HOST: ipAddressColName)", ErrInvalidInput, typ)
			}
		}
		if def.QueryID == "" {
			return nil, fmt.Errorf("%w: create needs queryId, a query saved in the organization's library (find-nqe-query)", ErrInvalidInput)
		}
		limits, err := checkCols(typ, def.QueryID, def.MetadataColNames, def.DeviceColName, def.InterfaceColName, def.IPAddressColName, def.HostColName, def.MACAddressColName)
		if err != nil {
			return nil, err
		}
		meta := def.MetadataColNames
		if meta == nil {
			meta = []string{} // all columns
		}
		put := forward.DecoratorPut{Type: typ, Name: name, Description: def.Description, QueryID: def.QueryID, MetadataColNames: &meta, Enabled: def.Enabled,
			DeviceColName: def.DeviceColName, InterfaceColName: def.InterfaceColName, IPAddressColName: def.IPAddressColName, HostColName: def.HostColName,
			MACAddressColName: def.MACAddressColName, HopIndexColName: def.HopIndexColName, DirectionColName: def.DirectionColName}
		return &networkPlan{target: fmt.Sprintf("create %s decorator %q on query %s", typ, name, def.QueryID), action: "create_decorator", after: put, reversible: true,
			undo:   "disable it (update with enabled false); delete is not offered here",
			limits: append(limits, "a new decorator is created disabled unless enabled is given; a user decorator runs whether or not it is enabled"),
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Decorators.Put(ctx, name, put)
				return err
			},
			verify: verify(func(d *forward.Decorator) bool { return d.QueryID == def.QueryID })}, nil
	case "update":
		if cur == nil {
			return nil, nil
		}
		if cur.SystemManaged || systemManagedDecorator(cur.Name) {
			return nil, fmt.Errorf("%w: %s is one of Forward's own decorators: it can only be enabled or disabled", ErrInvalidInput, cur.Name)
		}
		if def.Type != "" && !strings.EqualFold(def.Type, string(cur.Type)) {
			return nil, fmt.Errorf("%w: a decorator's type cannot change (it is %s): create another one", ErrInvalidInput, cur.Type)
		}
		pick := func(a, b string) string { return firstNonEmpty(a, b) }
		meta := cur.MetadataColNames
		if def.MetadataColNames != nil {
			meta = def.MetadataColNames
		}
		limits, err := checkCols(cur.Type, pick(def.QueryID, cur.QueryID), meta, pick(def.DeviceColName, cur.DeviceColName), pick(def.InterfaceColName, cur.InterfaceColName),
			pick(def.IPAddressColName, cur.IPAddressColName), pick(def.HostColName, cur.HostColName), pick(def.MACAddressColName, cur.MACAddressColName))
		if err != nil {
			return nil, err
		}
		put := forward.DecoratorPut{Type: cur.Type, Description: def.Description, QueryID: def.QueryID, Enabled: def.Enabled, DeviceColName: def.DeviceColName,
			InterfaceColName: def.InterfaceColName, IPAddressColName: def.IPAddressColName, HostColName: def.HostColName, MACAddressColName: def.MACAddressColName,
			HopIndexColName: def.HopIndexColName, DirectionColName: def.DirectionColName}
		if def.MetadataColNames != nil {
			put.MetadataColNames = &def.MetadataColNames
		}
		before, _ := fwd.Generic(*cur)
		return &networkPlan{target: fmt.Sprintf("change decorator %q", cur.Name), action: "update_decorator", before: before, after: put, reversible: true,
			undo:   "update again with the before values",
			limits: append(limits, "only the fields given change; the description and a host decorator's optional columns cannot be cleared here, only set to another value"),
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Decorators.Put(ctx, cur.Name, put)
				return err
			},
			verify: verify(func(d *forward.Decorator) bool { return def.QueryID == "" || d.QueryID == def.QueryID })}, nil
	case "enable", "disable":
		if cur == nil {
			return nil, fmt.Errorf("%w: no decorator named %q (inspect-platform area decorators lists them)", ErrInvalidInput, name)
		}
		on := in.Action == "enable"
		if cur.Enabled == on {
			return &networkPlan{target: fmt.Sprintf("%s decorator %q", in.Action, cur.Name), action: in.Action + "_decorator", noop: fmt.Sprintf("decorator %q is already %sd", cur.Name, in.Action)}, nil
		}
		managed := cur.SystemManaged || systemManagedDecorator(cur.Name)
		lim := []string{"a user decorator runs whether or not it is enabled (Forward's decorate call runs it when named); enabled is what the Forward views show"}
		if managed {
			lim = []string{"Forward's own decorators run only when enabled"}
		}
		return &networkPlan{target: fmt.Sprintf("%s decorator %q", in.Action, cur.Name), action: in.Action + "_decorator", before: map[string]any{"enabled": cur.Enabled}, after: map[string]any{"enabled": on},
			reversible: true, undo: "the opposite action", limits: lim,
			do: func(ctx context.Context) error {
				if managed {
					_, err := s.Client.Decorators.SetManagedEnabled(ctx, cur.Name, on)
					return err
				}
				_, _, err := s.Client.Decorators.Put(ctx, cur.Name, forward.DecoratorPut{Type: cur.Type, Enabled: &on})
				return err
			},
			verify: verify(func(d *forward.Decorator) bool { return d.Enabled == on })}, nil
	}
	return nil, fmt.Errorf("%w: area decorators takes action create, update, enable or disable", ErrInvalidInput)
}
