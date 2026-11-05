package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
)

// decoratorColumns are the columns a decorator of this type joins on (with the NQE type Forward needs) and the ones it shows.
func decoratorColumns(typ forward.DecoratorEntityType, device, iface, ip, host, mac string, metadata []string) []nqelint.DecoratorColumn {
	var cols []nqelint.DecoratorColumn
	add := func(name, t, role string) {
		if name != "" {
			cols = append(cols, nqelint.DecoratorColumn{Name: name, Type: t, Role: role})
		}
	}
	switch typ {
	case forward.DecoratorEntityDevice:
		add(device, "String", "device name join")
	case forward.DecoratorEntityInterface:
		add(device, "String", "device name join")
		add(iface, "String", "interface name join")
	case forward.DecoratorEntityHost:
		add(ip, "IpAddress", "IP address join")
		add(host, "String", "host name join")
		add(device, "String", "device name join")
		add(mac, "MacAddress", "MAC address join")
	}
	for _, m := range metadata {
		add(m, "", "shown")
	}
	return cols
}

// checkDecoratorQuery reads the saved query a decorator uses and checks its rows against the decorator's columns offline. It returns the
// problems, or a note saying why it could not check (a path decorator, a query it cannot read).
func checkDecoratorQuery(ctx context.Context, s *fwd.Session, typ forward.DecoratorEntityType, queryID string, cols []nqelint.DecoratorColumn) (problems []string, note string) {
	switch typ {
	case forward.DecoratorEntityDevice, forward.DecoratorEntityInterface, forward.DecoratorEntityHost:
	default:
		return nil, "not checked: a path decorator runs once per path with a Path parameter, and Forward never reports its columns as invalid"
	}
	src, found, err := s.OrgQuerySourceByID(ctx, "", queryID)
	switch {
	case err != nil:
		return nil, "not checked: the query's source could not be read (" + err.Error() + ")"
	case !found:
		return nil, fmt.Sprintf("query %s is not in the organization's library at the head commit: Forward refuses a decorator on it (400)", queryID)
	}
	for _, d := range nqelint.CheckDecoratorColumns(src, cols) {
		problems = append(problems, d.Message)
	}
	for _, d := range nqelint.Lint(src) {
		if d.Severity == "error" {
			problems = append(problems, "the query does not lint: "+d.Message)
			break
		}
	}
	return problems, ""
}

// systemManagedDecorator says Forward's own decorators ("fwd-..."): they can be enabled or disabled, never edited or deleted.
func systemManagedDecorator(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "fwd-")
}
