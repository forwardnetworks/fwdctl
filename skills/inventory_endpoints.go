package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// invEndpointRows reads every network endpoint of a snapshot with its profile and its collection result in that snapshot.
const invEndpointRows = `endpointResult(r) =
  when r is
    collectionFailed(e) -> "collectionFailed";
    processingFailed(e) -> "processingFailed";
    completed -> "completed";

foreach e in network.endpoints
select {
  Name: e.name,
  Profile: e.profileName,
  Location: e.locationName,
  Result: endpointResult(e.snapshotInfo.result)
}`

// inventoryEndpoints is kind endpoints: the snapshot's endpoints rolled up by profile (how many, how many collected, how many not, with the result), the first not-collected endpoints, and how many configured
// endpoints are switched off. It is one read, so a network with thousands of endpoints needs no paging; `name` limits it to profiles whose name contains the text.
func inventoryEndpoints(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	rows, nqeTotal, truncated, err := s.RunNQEAll(ctx, in.NetworkID, fwd.SnapshotID(cx), invEndpointRows, 20000)
	if err != nil {
		return result.Result{}, err
	}
	if truncated {
		limits = append(limits, fmt.Sprintf("this read the first %d of %d endpoints, so every count here is low", len(rows), nqeTotal))
	}
	if len(rows) == 0 {
		return result.NewUnknown(inventoryName, "This snapshot has no endpoints, so there is nothing to roll up", cx,
			append(limits, "no endpoint was in the snapshot's data model: none is configured, or none was collected"), result.Options{NextActions: []string{"inspect-collection"}})
	}
	filter := strings.ToLower(strings.TrimSpace(in.Name))
	type roll struct {
		endpoints, collected int
		results              map[string]int
	}
	by, order := map[string]*roll{}, []string{}
	var bad []map[string]any
	total, notCollected := 0, 0
	for _, r := range rows {
		profile := str(r["Profile"])
		if filter != "" && !strings.Contains(strings.ToLower(profile), filter) {
			continue
		}
		g := by[profile]
		if g == nil {
			g = &roll{results: map[string]int{}}
			by[profile] = g
			order = append(order, profile)
		}
		res := str(r["Result"])
		g.endpoints++
		total++
		if res == "completed" {
			g.collected++
			continue
		}
		g.results[res]++
		notCollected++
		bad = append(bad, map[string]any{"name": str(r["Name"]), "profile": profile, "location": str(r["Location"]), "result": res})
	}
	if total == 0 {
		return result.NewUnknown(inventoryName, fmt.Sprintf("No endpoint has a profile containing %q", in.Name), cx, append(limits, "name matches the endpoint profile name, as a case-insensitive substring"), result.Options{})
	}
	sort.SliceStable(order, func(i, j int) bool {
		ni, nj := by[order[i]].endpoints-by[order[i]].collected, by[order[j]].endpoints-by[order[j]].collected
		if ni != nj {
			return ni > nj
		}
		return order[i] < order[j]
	})
	profiles := make([]map[string]any, 0, len(order))
	for _, p := range order {
		g := by[p]
		profiles = append(profiles, map[string]any{"profile": p, "endpoints": g.endpoints, "collected": g.collected, "not_collected": g.endpoints - g.collected, "results": g.results})
	}
	shown, capped := result.Cap(bad, 25, "endpoints that were not collected", "the first 25 are listed")
	detail := map[string]any{"endpoints": total, "collected": total - notCollected, "not_collected_count": notCollected, "profiles": profiles, "not_collected": shown}
	// configured endpoints switched off never reach a snapshot as collected; counting them says why a profile's count may be short
	if cfg, cerr := s.Endpoints(ctx, in.NetworkID); cerr != nil {
		limits = append(limits, "the configured endpoints could not be read, so switched-off ones are not counted: "+cerr.Error())
	} else {
		off := 0
		for _, e := range cfg {
			if e.Collect != nil && !*e.Collect {
				off++
			}
		}
		detail["configured"] = len(cfg)
		detail["configured_collect_off"] = off
	}
	finding := fmt.Sprintf("%d endpoints in %d profile(s), all collected", total, len(profiles))
	next := []string{"inspect-collection"}
	if notCollected > 0 {
		finding = fmt.Sprintf("%d endpoints in %d profile(s), %d not collected", total, len(profiles), notCollected)
		next = []string{"inspect-collection", "investigate-collection-failure"}
		limits = append(limits, "collectionFailed and processingFailed are the snapshot's own result for the endpoint; the reason is in investigate-collection-failure. A profile whose commands the organization has not approved is not collected (edit-platform area endpoint_profiles says which)")
	}
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: capped, NextActions: next,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, finding)}})
}
