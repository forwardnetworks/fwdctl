package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// threatExposureInput is inspect-vulnerabilities' threat_exposure_experimental option: Forward's threat exposure feature, read only. EXPERIMENTAL: the routes exist only in newer Forward builds, the organization property
// THREAT_EXPOSURE enables the feature, and the shapes change between builds.
type threatExposureInput struct {
	// View: definitions, definition, status, critical_assets, direct, transitive or edge_assets.
	View         string `json:"view"`
	DefinitionID string `json:"definition_id"`
	AssetID      int64  `json:"asset_id"` // the global host id a critical_assets row gives the asset
	Offset       int    `json:"offset"`
	Limit        int    `json:"limit"`
}

func parseThreatExposure(raw json.RawMessage, in vulnInput) (threatExposureInput, bool, error) {
	var probe struct {
		TE *threatExposureInput `json:"threat_exposure_experimental"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return threatExposureInput{}, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if probe.TE == nil {
		return threatExposureInput{}, false, nil
	}
	t := *probe.TE
	t.View = strings.ToLower(strings.TrimSpace(t.View))
	if t.View == "" {
		t.View = "definitions"
	}
	switch t.View {
	case "definitions", "edge_assets":
	case "definition", "status", "critical_assets", "resolve":
		if strings.TrimSpace(t.DefinitionID) == "" {
			return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental view %s needs definition_id (view definitions lists them)", ErrInvalidInput, t.View)
		}
	case "direct", "transitive":
		if strings.TrimSpace(t.DefinitionID) == "" || t.AssetID == 0 {
			return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental view %s needs definition_id and asset_id (view critical_assets gives the asset ids)", ErrInvalidInput, t.View)
		}
	default:
		return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental.view is definitions, definition, status, critical_assets, resolve, direct, transitive or edge_assets", ErrInvalidInput)
	}
	if in.CVEID != "" || in.Device != "" || in.View != "" || in.MinSeverity != "" || in.KnownExploitedOnly || in.InternetAddressable != nil {
		return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental is a different question from the CVE inputs; give one", ErrInvalidInput)
	}
	return t, true, nil
}

var threatExposureLimits = []string{
	"EXPERIMENTAL: Forward's threat exposure feature is experimental. It is enabled by the organization property THREAT_EXPOSURE (default off; an on-premises organization administrator or Forward support can change it, on SaaS only support), its routes exist only in newer Forward builds (an older one answers 404 and this view says it does not serve them), and its shapes change between builds",
	"this view only reads; creating, changing or deleting definitions, starting an analysis and changing asset criticality are edit-network objects threat_exposure (create, update, delete, evaluate) and asset_criticality. The analysis views are Forward's raw answer",
	"it needs VIEW_THREAT_EXPOSURE (edge assets: VIEW_COLLECTION_SOURCES) and the security licence facet",
}

// threatExposure answers threat_exposure_experimental: reads only.
func threatExposure(ctx context.Context, s *fwd.Session, in vulnInput, t threatExposureInput, cx result.Context, snapID string) (result.Result, error) {
	te := s.Client.ThreatExposure
	limits := append([]string(nil), threatExposureLimits...)
	var omitted []result.Omission
	detail := map[string]any{"experimental": true, "view": t.View, "snapshot_id": snapID}
	fail := func(err error) (result.Result, error) {
		if errors.Is(err, forward.ErrEndpointNotServed) {
			return result.NewUnknown(vulnerabilitiesName, "This Forward build does not serve the threat exposure routes (EXPERIMENTAL feature), so nothing is known from here", cx,
				append(limits, "the routes exist only in newer Forward builds; the organization property THREAT_EXPOSURE also has to be on"), result.Options{})
		}
		if analysisUnavailable(err) && t.View != "definitions" && t.View != "definition" && t.View != "edge_assets" {
			return result.NewUnknown(vulnerabilitiesName, "The threat exposure analysis is not available on this Forward build, so nothing is known from here (EXPERIMENTAL feature)", cx,
				append(limits, analysisUnavailableLimit), result.Options{})
		}
		if r, ok := denialResult(vulnerabilitiesName, err, cx, "threat exposure"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	var finding string
	switch t.View {
	case "definitions":
		defs, _, err := te.ListDefinitions(ctx, in.NetworkID)
		if err != nil {
			return fail(err)
		}
		rows := make([]map[string]any, 0, len(defs))
		for _, d := range defs {
			rows = append(rows, map[string]any{"id": d.Definition.ID, "name": d.Definition.Name, "exposure_points": d.Definition.ExposurePoints, "locations": d.Definition.Locations,
				"criticalities": d.Definition.Criticalities, "subnets": d.Definition.Subnets, "max_duration_seconds": d.Definition.MaxDurationSeconds})
		}
		detail["definitions"] = rows
		finding = fmt.Sprintf("%d threat exposure definition(s) (EXPERIMENTAL)", len(rows))
		limits = append(limits, "latestAnalyzedSnapshotId is not filled in by Forward yet; use view status with a snapshot to see whether a definition was analysed on it")
	case "definition":
		d, _, err := te.GetDefinition(ctx, in.NetworkID, t.DefinitionID)
		if err != nil {
			return fail(err)
		}
		detail["definition"] = d
		finding = fmt.Sprintf("Threat exposure definition %s (%s) (EXPERIMENTAL)", d.ID, d.Name)
	case "resolve":
		// what a saved definition selects on this snapshot (its exposure points and the critical assets that match), read now: a POST that evaluates and stores nothing
		d, _, err := te.GetDefinition(ctx, in.NetworkID, t.DefinitionID)
		if err != nil {
			return fail(err)
		}
		out, _, err := te.ResolveDefinition(ctx, in.NetworkID, snapID, forward.NewThreatExposureDefinition{Name: d.Name, ExposurePoints: d.ExposurePoints, Locations: d.Locations, Criticalities: d.Criticalities, Subnets: d.Subnets, MaxDurationSeconds: d.MaxDurationSeconds})
		if err != nil {
			return fail(err)
		}
		detail["resolved"] = json.RawMessage(out)
		finding = fmt.Sprintf("What definition %s (%s) selects on snapshot %s (EXPERIMENTAL)", d.ID, d.Name, snapID)
		limits = append(limits, "resolve previews the definition on this snapshot and writes nothing: it shows the exposure points and the critical assets (edge assets with a criticality that are IN this snapshot) the definition matches; an edge asset added after the snapshot was created is not among them (see view edge_assets). The snapshot must be processed to the reachability stage. Forward's body is passed through unchanged")
	case "status":
		out, _, err := te.Status(ctx, in.NetworkID, t.DefinitionID, snapID)
		if err != nil {
			return fail(err)
		}
		detail["status"] = json.RawMessage(out)
		finding = fmt.Sprintf("Analysis status of definition %s on snapshot %s (EXPERIMENTAL)", t.DefinitionID, snapID)
	case "critical_assets":
		out, _, err := te.CriticalAssetsSummary(ctx, in.NetworkID, t.DefinitionID, snapID, forward.ThreatExposurePaging{Offset: t.Offset, Limit: threatLimit(t.Limit)})
		if err != nil {
			return fail(err)
		}
		detail["critical_assets"] = json.RawMessage(out)
		finding = fmt.Sprintf("Critical assets exposed under definition %s on snapshot %s (EXPERIMENTAL)", t.DefinitionID, snapID)
		limits = append(limits, "rows come in a fixed order (no sort); page with offset and limit")
	case "direct":
		out, _, err := te.DirectExposureDetails(ctx, in.NetworkID, t.DefinitionID, snapID, t.AssetID)
		if err != nil {
			return fail(err)
		}
		detail["direct_exposure"] = json.RawMessage(out)
		finding = fmt.Sprintf("Direct exposure of asset %d under definition %s (EXPERIMENTAL)", t.AssetID, t.DefinitionID)
	case "transitive":
		out, _, err := te.TransitiveExposureDetails(ctx, in.NetworkID, t.DefinitionID, snapID, t.AssetID, forward.ThreatExposurePaging{Offset: t.Offset, Limit: threatLimit(t.Limit)})
		if err != nil {
			return fail(err)
		}
		detail["transitive_exposure"] = json.RawMessage(out)
		finding = fmt.Sprintf("Transitive exposure of asset %d under definition %s (EXPERIMENTAL)", t.AssetID, t.DefinitionID)
	case "edge_assets":
		assets, _, err := s.Client.EdgeAssets.List(ctx, in.NetworkID)
		if err != nil {
			return fail(err)
		}
		detail["edge_assets"] = assets
		finding = fmt.Sprintf("%d edge asset(s) (EXPERIMENTAL)", len(assets))
		limits = append(limits, "edge assets are created in Forward from inferred hosts; their criticality (BUSINESS_CRITICAL, CRITICAL_INFRA, CROWN_JEWEL) is what a threat exposure definition selects on")
		// An asset is copied into a snapshot only when the snapshot is CREATED (Forward: SnapshotCreationStarter.saveEdgeAssets writes the included assets, when EDGE_ASSETS_IN_SOURCES is on, as a stored file);
		// a reprocess re-reads that stored file. An asset added or changed later is listed with includedInLatestSnapshot false, and threat exposure on that snapshot cannot see it.
		var late []string
		withCrit, lateCrit := 0, 0
		for _, a := range assets {
			if a.Criticality != "" {
				withCrit++
			}
			if a.Include && !a.IncludedInLatestSnapshot {
				late = append(late, a.Name)
				if a.Criticality != "" {
					lateCrit++
				}
			}
		}
		detail["edge_assets_with_criticality"] = withCrit
		// Forward maps edge assets to hosts by cloud host id, then MAC, then IP, and a MAC two assets share maps neither (a peer's live report: silently ignored)
		byMAC := map[string][]string{}
		for _, a := range assets {
			if a.Include && strings.TrimSpace(a.MACAddress) != "" {
				k := strings.ToLower(strings.TrimSpace(a.MACAddress))
				byMAC[k] = append(byMAC[k], a.Name)
			}
		}
		var dupMACs []map[string]any
		for mac, names := range byMAC {
			if len(names) > 1 {
				sort.Strings(names)
				dupMACs = append(dupMACs, map[string]any{"mac": mac, "assets": names})
			}
		}
		if len(dupMACs) > 0 {
			sort.Slice(dupMACs, func(i, j int) bool { return dupMACs[i]["mac"].(string) < dupMACs[j]["mac"].(string) })
			detail["edge_assets_sharing_a_mac"] = dupMACs
			limits = append(limits, fmt.Sprintf("%d MAC address(es) are shared by more than one included edge asset: a peer's live report says Forward maps NEITHER asset when a MAC is duplicated (no error, the asset never appears in the model); the report was on a snapshot package's file, so whether the same holds for assets added in the interface is not verified", len(dupMACs)))
		}
		detail["edge_assets_not_in_latest_snapshot"] = len(late)
		if len(late) > 0 {
			sort.Strings(late)
			shown, om := result.Cap(late, 50, "edge assets not in the latest snapshot", "the count is complete; list them all from detail.edge_assets (includedInLatestSnapshot false)")
			detail["edge_assets_not_in_latest_snapshot_names"] = shown
			omitted = append(omitted, om...)
			finding = fmt.Sprintf("%d edge asset(s) (EXPERIMENTAL); %d of them (%d with a criticality) are NOT in the latest snapshot, so threat exposure cannot see them", len(assets), len(late), lateCrit)
			limits = append(limits, fmt.Sprintf("%d included edge asset(s) were added or changed after the latest snapshot was created: Forward copies edge assets into a snapshot only when the snapshot is CREATED, and a reprocess re-reads what was stored then, so reprocessing does NOT add them. Create a NEW snapshot (a new collection) to include them; threat exposure on the latest snapshot reports no critical assets in scope until then, and the interface does not say why. Whether importing a snapshot package keeps the package's own edge-assets file or saves the network's current ones is not verified", len(late)))
		}
		if !a0IncludeAny(assets) && len(assets) > 0 {
			limits = append(limits, "no edge asset is marked include: only included assets are copied into a snapshot (and only while the organization property EDGE_ASSETS_IN_SOURCES is on at snapshot creation)")
		}
	}
	return result.Build(vulnerabilitiesName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "threatExposureExperimental", &snapID, detail, "EXPERIMENTAL")}, NextActions: []string{"inspect-vulnerabilities"}})
}

func threatLimit(n int) int {
	if n <= 0 {
		return 50
	}
	return min(n, 1000)
}

// a0IncludeAny says whether any edge asset is marked include (matched to hosts in later snapshots).
func a0IncludeAny(assets []forward.EdgeAsset) bool {
	for _, a := range assets {
		if a.Include {
			return true
		}
	}
	return false
}
