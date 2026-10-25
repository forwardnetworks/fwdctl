package skills

import (
	"context"
	"fmt"
	"sort"
	"sync"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const maxControllerSetupNetworks = 200

// platformControllerSetups lists the controller-managed setups of EVERY network and workspace the login can see, or of one when network_id is given, in the shape inspect-collection view config uses (controllerSetupRows):
// the controllers and managed devices with type, host, port, jump server and the stored login ids (never a value), and the last saved connectivity test. It adds the network each setup belongs to, so what is already
// configured can be reused before anyone proposes a new setup. Networks are read a few at a time.
func platformControllerSetups(ctx context.Context, s *fwd.Session, networkID string) ([]any, []string, error) {
	nets, err := s.Networks(ctx)
	if err != nil {
		return nil, nil, err
	}
	if networkID != "" {
		var one []forward.Network
		for _, n := range nets {
			if string(n.ID) == networkID {
				one = append(one, n)
			}
		}
		nets = one
	}
	var lim []string
	if len(nets) > maxControllerSetupNetworks {
		lim = append(lim, fmt.Sprintf("only the first %d of %d networks and workspaces were read", maxControllerSetupNetworks, len(nets)))
		sort.SliceStable(nets, func(i, j int) bool { return nets[i].Name < nets[j].Name }) // the same networks every time
		nets, _ = result.Cap(nets, maxControllerSetupNetworks, "networks", "name a network")
	}
	type found struct {
		rows []map[string]any
		err  error
	}
	res := make([]found, len(nets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, n := range nets {
		wg.Add(1)
		go func(i int, n forward.Network) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			setups, _, e := s.Client.ControllerManagedSetups.ListWithTestResults(ctx, string(n.ID))
			if e != nil {
				res[i].err = e
				return
			}
			rows, _ := controllerSetupRows(setups, collectionConfigInput{})
			for _, r := range rows {
				r["network_id"], r["network"], r["workspace"] = string(n.ID), n.Name, string(n.ParentID) != ""
			}
			res[i].rows = rows
		}(i, n)
	}
	wg.Wait()
	var out []any
	failed := 0
	for _, f := range res {
		if f.err != nil {
			failed++
			continue
		}
		for _, r := range f.rows {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return fmt.Sprint(out[i].(map[string]any)["network"]) < fmt.Sprint(out[j].(map[string]any)["network"])
	})
	if failed > 0 {
		lim = append(lim, fmt.Sprintf("%d network(s) could not be read (no access, or the controller-managed route is not served there); an empty list for them is not 'none'", failed))
	}
	lim = append(lim, fmt.Sprintf("read %d networks and workspaces; Forward lists an empty set on some deployments that do not support the feature, so no row can mean none or unsupported", len(nets)),
		"the shape is the one inspect-collection view config shows for a network (controller_setups): devices are the controllers and managed devices, up to 100 per setup; logins are ids only; which stored login works is in each device's last test (discovered logins)")
	return out, lim, nil
}
