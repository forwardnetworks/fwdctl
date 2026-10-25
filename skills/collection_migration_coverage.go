package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	controlConnectionsFile = "sdwan_control_connections.txt"
	maxCoverageEdges       = 300
)

// parseVSmartsUp reads `show sdwan control connections` text (3 header lines, a dashed line, then fixed-width rows) and returns the system IPs of the vsmart peers whose STATE is up, and how many vsmart rows there were.
// A row is read by whitespace: PEER TYPE is field 1, PEER SYSTEM IP field 3, and STATE is the token after PROXY (Yes/No) found from the right, because ORGANIZATION may contain spaces and a row can lack the trailing CONTROLLER GROUP ID. A vsmart appears once per local color, so the same system IP repeats.
func parseVSmartsUp(text string) (up []string, vsmartRows int) {
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 13 || !strings.EqualFold(f[0], "vsmart") {
			continue
		}
		vsmartRows++
		// STATE is the token after PROXY (Yes/No), found from the right: a row can lack the trailing CONTROLLER GROUP ID, so a fixed offset from the end is not safe
		state := ""
		for i := len(f) - 2; i >= 9; i-- {
			if strings.EqualFold(f[i], "yes") || strings.EqualFold(f[i], "no") {
				state = f[i+1]
				break
			}
		}
		if strings.EqualFold(state, "up") && !seen[f[2]] {
			seen[f[2]] = true
			up = append(up, f[2])
		}
	}
	sort.Strings(up)
	return up, vsmartRows
}

// migrationCoverage reads each IOS XE SD-WAN edge's control connections from the newest processed snapshot and answers what controller mode needs to know: which vSmart system IPs each edge is connected to (state up),
// the smallest set of vSmarts covering every edge (greedy), the edges with no vSmart rows or none up, and which edges the wizard's controllers (the addresses the connectivity tests found) do not reach.
func migrationCoverage(ctx context.Context, s *fwd.Session, networkID string, edges []string, wizard []string) (map[string]any, []string) {
	var limits []string
	if len(edges) == 0 {
		return nil, nil
	}
	snap, err := resolveSnapshot(ctx, s, networkID, "")
	if err != nil || snap == nil || !fwd.IsReady(snap) {
		return nil, []string{"edge coverage was not computed: no processed snapshot to read the control connections from"}
	}
	sid := string(snap.ID)
	if len(edges) > maxCoverageEdges {
		limits = append(limits, fmt.Sprintf("coverage read the first %d of %d edges", maxCoverageEdges, len(edges)))
		edges, _ = result.Cap(edges, maxCoverageEdges, "edges", "narrow the view")
	}
	type edgeRead struct {
		up      []string
		rows    int
		missing bool
		err     error
	}
	reads := make([]edgeRead, len(edges))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, e := range edges {
		wg.Add(1)
		go func(i int, e string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data, _, derr := s.DeviceFile(ctx, networkID, e, controlConnectionsFile, sid, 4<<20)
			switch {
			case fwd.NotFound(derr):
				reads[i].missing = true
			case derr != nil:
				reads[i].err = derr
			default:
				reads[i].up, reads[i].rows = parseVSmartsUp(string(data))
			}
		}(i, e)
	}
	wg.Wait()
	edgeUp := map[string][]string{}
	var noFile, noRows, noneUp, failed []string
	for i, e := range edges {
		r := reads[i]
		switch {
		case r.err != nil:
			failed = append(failed, e)
		case r.missing:
			noFile = append(noFile, e)
		case r.rows == 0:
			noRows = append(noRows, e)
		case len(r.up) == 0:
			noneUp = append(noneUp, e)
		default:
			edgeUp[e] = r.up
		}
	}
	// greedy minimal cover of the edges that have a vsmart up
	left := map[string]bool{}
	for e := range edgeUp {
		left[e] = true
	}
	var cover []map[string]any
	for len(left) > 0 {
		gain := map[string][]string{}
		for e := range left {
			for _, v := range edgeUp[e] {
				gain[v] = append(gain[v], e)
			}
		}
		best, bestN := "", 0
		for v, es := range gain {
			if len(es) > bestN || (len(es) == bestN && v < best) {
				best, bestN = v, len(es)
			}
		}
		if best == "" {
			break
		}
		covered := gain[best]
		sort.Strings(covered)
		for _, e := range covered {
			delete(left, e)
		}
		cover = append(cover, map[string]any{"vsmart_system_ip": best, "newly_covered_edges": len(covered)})
	}
	wiz := map[string]bool{}
	for _, a := range wizard {
		if a != "" {
			wiz[a] = true
		}
	}
	var notReached []string
	for e, ups := range edgeUp {
		reached := false
		for _, v := range ups {
			if wiz[v] {
				reached = true
			}
		}
		if !reached {
			notReached = append(notReached, e)
		}
	}
	sort.Strings(notReached)
	cap := func(xs []string) any {
		shown, _ := result.CapRow(xs, 50)
		return map[string]any{"count": len(xs), "names": shown}
	}
	distinct := map[string]bool{}
	for _, ups := range edgeUp {
		for _, v := range ups {
			distinct[v] = true
		}
	}
	out := map[string]any{"snapshot_id": sid, "edges_read": len(edges), "edges_with_a_vsmart_up": len(edgeUp), "distinct_vsmarts_up": len(distinct), "minimal_cover": cover,
		"edges_with_no_control_connections_file": cap(noFile), "edges_with_no_vsmart_rows": cap(noRows), "edges_with_no_vsmart_up": cap(noneUp), "wizard_controllers": wizard,
		"edges_the_wizard_controllers_do_not_reach": cap(notReached)}
	if len(failed) > 0 {
		out["edges_unreadable"] = cap(failed)
	}
	limits = append(limits, "coverage reads sdwan_control_connections.txt (show sdwan control connections) of each IOS XE SD-WAN edge in snapshot "+sid+", keeps vsmart rows whose STATE is up, and takes PEER SYSTEM IP as the vSmart; the file's layout is from one field sample; a vSmart appears once per local color",
		"the minimal cover is a greedy choice, not proven smallest; the wizard's controllers are the addresses the devices' last connectivity tests reported, which are overlay system IPs and may not be reachable from the collector",
		"an edge receives OMP data only from a controller you add that it is connected to (source-read, not tested): partial coverage silently loses OMP data for the edges it does not reach")
	return out, limits
}
