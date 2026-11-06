package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// maxTracePaths bounds how many distinct paths explain trace traces (one request each).
const maxTracePaths = 16

// traceLimit is said on every explain trace answer: the routes are Forward's GUI flow trace, not its published API.
const traceLimit = "explain trace is PREVIEW and UNDOCUMENTED: it reads Forward's GUI flow-trace routes, which are not in Forward's published API and can change between Forward builds without notice. Each path's `trace` is Forward's response body passed through unchanged: deviceHops with inPorts/outPorts (none on a hop that dropped or ended it), headers (the packet AFTER the hop; ingressHeaders for the packet entering the first), networkFunctions by layer (INPUT, L2, L3, ACCESS_CONTROL, NAT, OUTPUT) with fileLines (0-based line ranges in the collected file; add 1 for an editor) and actionBuckets (SET_FIELD rewrites, PERMIT or DENY), and behavior.processingStages per hop. Values can be symbolic (a MAC of host_mac, a NAT source of \"Egress Interface IP\"). Forward groups paths by device path, so parallel links or different behaviours on one device path are one entry (its sample is traced); dropTables was never seen in a live response; IPv4 only. The trace places the source the way Forward's GUI does (the subnet of src_ip, or the device in from), which can differ from the path search above: when the two disagree (another first hop, other outcomes), compare their paths before relying on either, and give from to pin the source"

// explainTrace lists the distinct paths of the flow with Forward's trace and traces each (up to maxTracePaths), returning one evidence that holds
// them and the limits to add. Nothing it reads changes the path-search answer: a trace that cannot be read is a limit, never a failed result.
func explainTrace(ctx context.Context, s *fwd.Session, in reachabilityInput, snapshotID string, snapshot *string) (*result.Evidence, []string) {
	limits := []string{traceLimit}
	q := forward.TraceQuery{SrcIP: in.SrcIP, From: in.From, DstIP: in.DstIP, SrcPort: in.SrcPort, DstPort: in.DstPort}
	if p := protocolText(in.Protocol); p != "" {
		n, err := fwd.ParseProtocol(p)
		if err != nil {
			return nil, append(limits, "explain trace: "+err.Error()+"; no trace was read")
		}
		q.IPProto = n
	}
	facets, _, err := s.Client.Trace.Facets(ctx, in.NetworkID, snapshotID, q, forward.TraceFacetsOptions{})
	if err != nil {
		if errors.Is(err, forward.ErrEndpointNotServed) {
			return nil, append(limits, "explain trace: this Forward does not serve the GUI trace routes; no trace was read")
		}
		return nil, append(limits, "explain trace: the trace could not be read ("+err.Error()+")")
	}
	if len(facets.PathGroups) == 0 && len(facets.PossibleIPLocations) > 0 && string(facets.PossibleIPLocations) != "null" {
		return nil, append(limits, "explain trace: the source address is at more than one place; give from (the device it enters at) to trace it. Places: "+string(facets.PossibleIPLocations))
	}
	var paths []map[string]any
	total, truncated, failed := 0, false, 0
	for gi, g := range facets.PathGroups {
		for pi := range g.Paths {
			total++
			if len(paths) >= maxTracePaths {
				truncated = true
				continue
			}
			sel, _ := g.Selection(pi)
			row := map[string]any{"id": fmt.Sprintf("g%d.p%d", gi, pi), "group": gi, "index": pi, "trace_type": g.TraceType, "path": sel.Path}
			tr, _, terr := s.Client.Trace.Trace(ctx, in.NetworkID, snapshotID, q, sel)
			switch {
			case terr == nil:
				row["descriptor"] = nilIfEmpty(tr.PathDescriptor)
				row["trace"] = json.RawMessage(tr.Raw)
			case ctx.Err() != nil:
				return nil, append(limits, "explain trace: stopped: "+ctx.Err().Error())
			case forward.IsStatus(terr, http.StatusUnauthorized) || forward.IsStatus(terr, http.StatusForbidden) || forward.IsStatus(terr, http.StatusNotFound) ||
				forward.IsStatus(terr, http.StatusTooManyRequests) || errors.Is(terr, forward.ErrEndpointNotServed):
				return nil, append(limits, "explain trace: the trace was refused ("+terr.Error()+"); no trace was read")
			default:
				row["error"] = terr.Error()
				failed++
			}
			paths = append(paths, row)
		}
	}
	lowerBound := false
	if len(facets.TotalResults) > 0 {
		var t struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(facets.TotalResults, &t)
		lowerBound = t.Type == "LOWER_BOUND"
	}
	if truncated {
		limits = append(limits, fmt.Sprintf("explain trace traced %d of %d distinct paths; narrow the flow (ports, from) to trace the rest", maxTracePaths, total))
	}
	if facets.TimedOut || lowerBound {
		limits = append(limits, "explain trace: Forward ran out of time or capped the flows it examined while listing paths, so more paths may exist")
	}
	if failed > 0 {
		limits = append(limits, fmt.Sprintf("explain trace: %d path(s) could not be traced; each says why in error", failed))
	}
	if len(paths) == 0 {
		limits = append(limits, "explain trace: Forward's trace listed no paths for this flow")
	}
	// file_lines_base: Forward's fileLines count from 0, while inspect-device-files mode read counts from 1 (add 1 to a fileLines line to read it there)
	detail := map[string]any{"preview": true, "file_lines_base": 0, "paths": paths, "distinct_paths": total, "traced": len(paths) - failed, "truncated": truncated,
		"timed_out": facets.TimedOut, "results_lower_bound": lowerBound}
	ev := result.NewEvidence(result.EvPath, "trace", snapshot, detail, fmt.Sprintf("trace (preview): %d distinct path(s), %d traced", total, len(paths)-failed))
	return &ev, limits
}
