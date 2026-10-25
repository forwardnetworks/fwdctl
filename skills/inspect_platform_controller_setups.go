package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

const maxControllerSetupNetworks = 200

// platformControllerSetups lists the controller-managed setups of EVERY network and workspace the login can see (name, collector, controllers with host, port, type, jump server and which stored logins they use, the guests'
// count and the last saved connectivity test), so what is already configured can be reused before anyone proposes a new setup. Logins are ids, never values. Networks are read a few at a time.
func platformControllerSetups(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
	nets, err := s.Networks(ctx)
	if err != nil {
		return nil, nil, err
	}
	var lim []string
	if len(nets) > maxControllerSetupNetworks {
		lim = append(lim, fmt.Sprintf("only the first %d of %d networks and workspaces were read", maxControllerSetupNetworks, len(nets)))
		nets, _ = result2cap(nets, maxControllerSetupNetworks)
	}
	type found struct {
		rows []any
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
			for _, st := range setups {
				ctls := make([]map[string]any, 0, len(st.Controllers))
				tests := map[string]int{}
				latest := ""
				for _, c := range st.Controllers {
					cr := map[string]any{"name": c.Name, "type": nilIfEmpty(c.Type), "host": nilIfEmpty(c.Host), "jump_server_id": nilIfEmpty(c.JumpServerID),
						"cli_credential_id": nilIfEmpty(c.CLICredentialID), "http_credential_id": nilIfEmpty(c.HTTPCredentialID), "snmp_credential_id": nilIfEmpty(c.SNMPCredentialID)}
					if c.Port != nil {
						cr["port"] = *c.Port
					}
					outcome, at := setupTestOutcome(c.TestResult)
					cr["test"] = outcome
					if at != "" {
						cr["tested_at"] = at
						if at > latest {
							latest = at
						}
					}
					tests[strings.SplitN(outcome, ":", 2)[0]]++
					ctls = append(ctls, cr)
				}
				row := map[string]any{"network_id": string(n.ID), "network": n.Name, "workspace": string(n.ParentID) != "", "setup": st.Name, "controllers": ctls,
					"managed_devices": len(st.ManagedDevices), "controller_tests": tests}
				if latest != "" {
					row["last_controller_test"] = latest
				}
				if c := string(st.CollectorID); c != "" {
					row["collector_id"] = c
				}
				res[i].rows = append(res[i].rows, row)
			}
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
		out = append(out, f.rows...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return fmt.Sprint(out[i].(map[string]any)["network"]) < fmt.Sprint(out[j].(map[string]any)["network"])
	})
	if failed > 0 {
		lim = append(lim, fmt.Sprintf("%d network(s) could not be read (no access, or the controller-managed route is not served there); an empty list for them is not 'none'", failed))
	}
	lim = append(lim, fmt.Sprintf("read %d networks and workspaces; Forward lists an empty set on some deployments that do not support the feature, so no row can mean none or unsupported", len(nets)),
		"logins are ids only; which stored login works on a controller is not recorded (the last connectivity test result says whether it did)")
	return out, lim, nil
}

func result2cap[T any](xs []T, n int) ([]T, int) {
	if len(xs) <= n {
		return xs, len(xs)
	}
	return xs[:n:n], len(xs)
}
