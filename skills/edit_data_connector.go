package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editDataConnectorName = "edit-data-connector"

func init() { Register(editDataConnectorName, editDataConnector) }

type dcEndpointInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// dcRefs is CredentialID, ProxyServerID and CollectorID: add treats an empty one as unset. update is tri-state: a field absent from the raw
// JSON leaves it alone, present-and-empty clears it (Forward picks a Collector, or uses no credential/proxy), present-and-set sets it. Go's
// encoding/json cannot tell "absent" from "present and empty" for a plain string field, so update reads raw JSON (hasField) to tell them apart.
type dcRefs struct {
	CredentialID  string `json:"credential_id"`
	ProxyServerID string `json:"proxy_server_id"`
	CollectorID   string `json:"collector_id"`
}

type editDataConnectorInput struct {
	// Action: add, update, delete, test.
	Action    string `json:"action"`
	NetworkID string `json:"network_id"`
	// Name is the connector name: for add, the new one; for update/delete/test, an existing one, exact.
	Name string `json:"name"`
	// add/update:
	BaseURL              string            `json:"base_url"`
	DisableSSLValidation *bool             `json:"disable_ssl_validation"`
	Refs                 dcRefs            `json:"refs"`
	ExtraHeaders         map[string]string `json:"extra_headers"`
	Endpoints            []dcEndpointInput `json:"endpoints"`
	Collect              *bool             `json:"collect"`
	// Confirm must equal name to delete.
	Confirm string `json:"confirm"`
	Apply   bool   `json:"apply"`
}

func hasField(raw json.RawMessage, path ...string) bool {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	for _, p := range path[:len(path)-1] {
		v, ok := m[p]
		if !ok {
			return false
		}
		m = nil
		_ = json.Unmarshal(v, &m)
	}
	_, ok := m[path[len(path)-1]]
	return ok
}

func toSDKEndpoints(in []dcEndpointInput) []forward.HTTPEndpoint {
	out := make([]forward.HTTPEndpoint, 0, len(in))
	for _, e := range in {
		out = append(out, forward.HTTPEndpoint{Name: e.Name, Path: e.Path})
	}
	return out
}

func dcEvidence(action string, detail map[string]any) []result.Evidence {
	return []result.Evidence{result.NewEvidence(result.EvState, "dataConnectors", nil, detail, "")}
}

// editDataConnector manages a network's data connectors (per-network HTTP sources the Collector polls each collection, exposed to NQE as
// network.dataConnectors), unlike a data file (edit-data-file), which is organization-wide: add, update, delete, or run a connectivity test.
// This skill does not set a connector's paginationModel (an endpoint's pagination shape varies by type); it manages static endpoints only.
func editDataConnector(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editDataConnectorInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" || in.Name == "" {
		return result.Result{}, fmt.Errorf("%w: network_id and name are required", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	if u, err := url.Parse(in.BaseURL); in.BaseURL != "" && (err != nil || u.Scheme == "" || u.Host == "") {
		return result.Result{}, fmt.Errorf("%w: base_url must be an absolute URL (scheme and host)", ErrInvalidInput)
	}
	switch in.Action {
	case "add":
		return planDataConnectorAdd(ctx, s, in, cx, mode)
	case "update":
		return planDataConnectorUpdate(ctx, s, in, raw, cx, mode)
	case "delete":
		return planDataConnectorDelete(ctx, s, in, cx, mode)
	case "test":
		return planDataConnectorTest(ctx, s, in, cx)
	default:
		return result.Result{}, fmt.Errorf("%w: action must be add, update, delete or test", ErrInvalidInput)
	}
}

func planDataConnectorAdd(ctx context.Context, s *fwd.Session, in editDataConnectorInput, cx result.Context, mode string) (result.Result, error) {
	if in.BaseURL == "" || len(in.Endpoints) == 0 {
		return result.Result{}, fmt.Errorf("%w: add needs base_url and at least one endpoint", ErrInvalidInput)
	}
	existing, err := s.DataConnector(ctx, in.NetworkID, in.Name, forward.DataConnectorReadOptions{})
	if err != nil {
		return result.Result{}, err
	}
	if existing != nil {
		return result.Build(editDataConnectorName, result.Failed, fmt.Sprintf("Refused, nothing was changed: a data connector named %q already exists on network %s; use update", in.Name, in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: dcEvidence("add", map[string]any{"name": in.Name, "found": true})})
	}
	after := map[string]any{"name": in.Name, "base_url": in.BaseURL, "endpoints": len(in.Endpoints), "collect": in.Collect == nil || *in.Collect}
	ch := result.Change{Action: "add_data_connector", Target: fmt.Sprintf("data connector %s on network %s", in.Name, in.NetworkID), Before: nil, After: after, Reversible: true, Undo: fmt.Sprintf("delete data connector %s from network %s", in.Name, in.NetworkID)}
	limits := []string{"this adds a per-network collection source; no other network is affected", "paginated endpoints are not supported by this skill; every endpoint given is treated as static"}
	if !in.Apply {
		return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Dry run: would add data connector %q to network %s (%d endpoint(s)). Nothing was changed; run again with apply=true", in.Name, in.NetworkID, len(in.Endpoints)),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: dcEvidence("add", after)})
	}
	created, err := s.AddDataConnector(ctx, in.NetworkID, forward.NewDataConnector{Name: in.Name, BaseURL: in.BaseURL, DisableSSLValidation: in.DisableSSLValidation, CredentialID: in.Refs.CredentialID, ProxyServerID: in.Refs.ProxyServerID, ExtraHeaders: in.ExtraHeaders, Endpoints: toSDKEndpoints(in.Endpoints), Collect: in.Collect, CollectorID: in.Refs.CollectorID})
	if err != nil {
		return result.Result{}, fmt.Errorf("adding the data connector failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	now, rerr := s.DataConnector(ctx, in.NetworkID, in.Name, forward.DataConnectorReadOptions{})
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the add was sent but reading the connector back failed, so it is not proven: %w", rerr)
	}
	if now == nil {
		return result.Build(editDataConnectorName, result.Failed, fmt.Sprintf("Forward accepted the add but data connector %q is not listed back", in.Name), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: dcEvidence("add", after)})
	}
	return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Added data connector %q to network %s (%d endpoint(s), read back)", created.Name, in.NetworkID, len(created.Endpoints)),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"inspect-collection"},
			Evidence: dcEvidence("add", map[string]any{"name": created.Name, "endpoints": len(created.Endpoints)})})
}

func planDataConnectorUpdate(ctx context.Context, s *fwd.Session, in editDataConnectorInput, raw json.RawMessage, cx result.Context, mode string) (result.Result, error) {
	existing, err := s.DataConnector(ctx, in.NetworkID, in.Name, forward.DataConnectorReadOptions{})
	if err != nil {
		return result.Result{}, err
	}
	if existing == nil {
		return result.Build(editDataConnectorName, result.Failed, fmt.Sprintf("Refused, nothing was changed: no data connector named %q on network %s", in.Name, in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: dcEvidence("update", map[string]any{"name": in.Name, "found": false})})
	}
	patch := forward.DataConnectorPatch{ExtraHeaders: in.ExtraHeaders, Collect: in.Collect, DisableSSLValidation: in.DisableSSLValidation}
	after := map[string]any{}
	if in.BaseURL != "" {
		patch.BaseURL = &in.BaseURL
		after["base_url"] = in.BaseURL
	}
	if len(in.Endpoints) > 0 {
		patch.Endpoints = toSDKEndpoints(in.Endpoints)
		after["endpoints"] = len(in.Endpoints)
	}
	for field, dst := range map[string]**string{"credential_id": &patch.CredentialID, "proxy_server_id": &patch.ProxyServerID, "collector_id": &patch.CollectorID} {
		if !hasField(raw, "refs", field) {
			continue
		}
		v := map[string]string{"credential_id": in.Refs.CredentialID, "proxy_server_id": in.Refs.ProxyServerID, "collector_id": in.Refs.CollectorID}[field]
		*dst = &v
		if v == "" {
			after[field] = nil
		} else {
			after[field] = v
		}
	}
	if in.DisableSSLValidation != nil {
		after["disable_ssl_validation"] = *in.DisableSSLValidation
	}
	if in.Collect != nil {
		after["collect"] = *in.Collect
	}
	if in.ExtraHeaders != nil {
		after["extra_headers_count"] = len(in.ExtraHeaders)
	}
	if len(after) == 0 {
		return result.Result{}, fmt.Errorf("%w: update needs at least one of base_url, endpoints, disable_ssl_validation, extra_headers, collect, credential_id, proxy_server_id, collector_id", ErrInvalidInput)
	}
	before := map[string]any{"base_url": existing.BaseURL, "endpoints": len(existing.Endpoints), "collect": existing.Collects()}
	ch := result.Change{Action: "update_data_connector", Target: fmt.Sprintf("data connector %s on network %s", in.Name, in.NetworkID), Before: before, After: after, Reversible: true, Undo: "update it again with the previous values (see before)"}
	limits := []string{"a cleared credential_id, proxy_server_id or collector_id (empty string) means: no credential, no proxy, or Forward picks a Collector", "paginated endpoints are not supported by this skill; endpoints given here replace the whole list as static entries"}
	if !in.Apply {
		return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Dry run: would update data connector %q on network %s. Nothing was changed; run again with apply=true", in.Name, in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: dcEvidence("update", before)})
	}
	updated, err := s.UpdateDataConnector(ctx, in.NetworkID, in.Name, patch)
	if err != nil {
		return result.Result{}, fmt.Errorf("the update failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Updated data connector %q on network %s (%d endpoint(s), read back)", updated.Name, in.NetworkID, len(updated.Endpoints)),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"inspect-collection"},
			Evidence: dcEvidence("update", map[string]any{"base_url": updated.BaseURL, "endpoints": len(updated.Endpoints)})})
}

func planDataConnectorDelete(ctx context.Context, s *fwd.Session, in editDataConnectorInput, cx result.Context, mode string) (result.Result, error) {
	existing, err := s.DataConnector(ctx, in.NetworkID, in.Name, forward.DataConnectorReadOptions{})
	if err != nil {
		return result.Result{}, err
	}
	if existing == nil {
		return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Data connector %q is already gone from network %s; nothing to change", in.Name, in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: dcEvidence("delete", map[string]any{"name": in.Name, "found": false})})
	}
	before := map[string]any{"name": existing.Name, "base_url": existing.BaseURL, "endpoints": len(existing.Endpoints)}
	ch := result.Change{Action: "delete_data_connector", Target: fmt.Sprintf("data connector %s on network %s", in.Name, in.NetworkID), Before: before, After: nil, Reversible: true,
		Undo: "add it back with the same base_url and endpoints (see before); its stored test result and collection history are not restored"}
	limits := []string{"deleting loses the connector's stored test result; the connector's past collected data stays in earlier snapshots"}
	if !strings.EqualFold(strings.TrimSpace(in.Confirm), in.Name) {
		return result.Build(editDataConnectorName, result.Failed, fmt.Sprintf("Refused: delete needs confirm set to the connector's exact name (%q)", in.Name), result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: dcEvidence("delete", before)})
	}
	if !in.Apply {
		return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Dry run: would delete data connector %q from network %s. Nothing was changed; run again with apply=true", in.Name, in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: dcEvidence("delete", before)})
	}
	if err := s.DeleteDataConnector(ctx, in.NetworkID, in.Name); err != nil {
		return result.Result{}, fmt.Errorf("the delete failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	now, rerr := s.DataConnector(ctx, in.NetworkID, in.Name, forward.DataConnectorReadOptions{})
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the delete was sent but reading it back failed, so it is not proven: %w", rerr)
	}
	if now != nil {
		return result.Build(editDataConnectorName, result.Failed, fmt.Sprintf("Forward accepted the delete but %q is still listed", in.Name), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: dcEvidence("delete", before)})
	}
	return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Deleted data connector %q from network %s (read back)", in.Name, in.NetworkID),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: dcEvidence("delete", before)})
}

func planDataConnectorTest(ctx context.Context, s *fwd.Session, in editDataConnectorInput, cx result.Context) (result.Result, error) {
	if in.Apply {
		return result.Result{}, fmt.Errorf("%w: test is a read-only connectivity check, not a write; apply does not apply to it", ErrInvalidInput)
	}
	existing, err := s.DataConnector(ctx, in.NetworkID, in.Name, forward.DataConnectorReadOptions{})
	if err != nil {
		return result.Result{}, err
	}
	if existing == nil {
		return result.Build(editDataConnectorName, result.Failed, fmt.Sprintf("Refused: no data connector named %q on network %s", in.Name, in.NetworkID), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Evidence: dcEvidence("test", map[string]any{"name": in.Name, "found": false})})
	}
	testCtx, cancel := context.WithTimeout(ctx, forward.DataConnectorTestTimeout+10*time.Second)
	defer cancel()
	res, err := s.TestDataConnector(testCtx, in.NetworkID, in.Name)
	if err != nil {
		return result.Result{}, fmt.Errorf("the test call failed (this is Forward or the network failing to run the test, not the test's own connectivity result): %w", err)
	}
	limits := []string{"this calls out from Forward's Collector right now and blocks until it answers (up to 60s), unlike every other action here, which only changes Forward's own stored configuration",
		"not a pure read: Forward replaces the connector's stored test result with this outcome (no config or collection changes), so inspect-collection view config now shows this run, not the previous one"}
	if res.Error != "" {
		return result.Build(editDataConnectorName, result.Failed, fmt.Sprintf("Data connector %q failed its connectivity test: %s (%s). Its stored test result was updated to this outcome", in.Name, res.Error, res.ErrorDesc), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Limits: limits, Evidence: dcEvidence("test", map[string]any{"error": res.Error, "error_desc": res.ErrorDesc})})
	}
	return result.Build(editDataConnectorName, result.OK, fmt.Sprintf("Data connector %q passed its connectivity test (%s to %s). Its stored test result was updated to this outcome", in.Name, res.StartedAt, res.EndedAt), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Limits: limits, Evidence: dcEvidence("test", map[string]any{"started_at": res.StartedAt, "ended_at": res.EndedAt})})
}
