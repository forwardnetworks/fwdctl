package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	case "definition", "status", "critical_assets":
		if strings.TrimSpace(t.DefinitionID) == "" {
			return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental view %s needs definition_id (view definitions lists them)", ErrInvalidInput, t.View)
		}
	case "direct", "transitive":
		if strings.TrimSpace(t.DefinitionID) == "" || t.AssetID == 0 {
			return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental view %s needs definition_id and asset_id (view critical_assets gives the asset ids)", ErrInvalidInput, t.View)
		}
	default:
		return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental.view is definitions, definition, status, critical_assets, direct, transitive or edge_assets", ErrInvalidInput)
	}
	if in.CVEID != "" || in.Device != "" || in.View != "" || in.MinSeverity != "" || in.KnownExploitedOnly || in.InternetAddressable != nil {
		return threatExposureInput{}, false, fmt.Errorf("%w: threat_exposure_experimental is a different question from the CVE inputs; give one", ErrInvalidInput)
	}
	return t, true, nil
}

var threatExposureLimits = []string{
	"EXPERIMENTAL: Forward's threat exposure feature is experimental. It is enabled by the organization property THREAT_EXPOSURE (default off; not an organization administrator's to change), its routes exist only in newer Forward builds (an older one answers 404 and this view says it does not serve them), and its shapes change between builds",
	"this view only reads; creating or changing definitions, running an analysis and changing asset criticality are not offered here. The analysis views are Forward's raw answer",
	"it needs VIEW_THREAT_EXPOSURE (edge assets: VIEW_COLLECTION_SOURCES) and the security licence facet",
}

// threatExposure answers threat_exposure_experimental: reads only.
func threatExposure(ctx context.Context, s *fwd.Session, in vulnInput, t threatExposureInput, cx result.Context, snapID string) (result.Result, error) {
	te := s.Client.ThreatExposure
	limits := append([]string(nil), threatExposureLimits...)
	detail := map[string]any{"experimental": true, "view": t.View, "snapshot_id": snapID}
	fail := func(err error) (result.Result, error) {
		if errors.Is(err, forward.ErrEndpointNotServed) {
			return result.NewUnknown(vulnerabilitiesName, "This Forward build does not serve the threat exposure routes (EXPERIMENTAL feature), so nothing is known from here", cx,
				append(limits, "the routes exist only in newer Forward builds; the organization property THREAT_EXPOSURE also has to be on"), result.Options{})
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
	}
	return result.Build(vulnerabilitiesName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "threatExposureExperimental", &snapID, detail, "EXPERIMENTAL")}, NextActions: []string{"inspect-vulnerabilities"}})
}

func threatLimit(n int) int {
	if n <= 0 {
		return 50
	}
	return min(n, 1000)
}
