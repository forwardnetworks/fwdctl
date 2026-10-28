package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

var firewallObjectKinds = []string{"address-objects", "address-groups", "application-objects", "application-groups", "url-objects", "service-objects", "service-groups", "user-objects", "zones"}

var predictProtocols = []string{"BGP", "OSPF", "IS_IS", "EIGRP", "RIP", "CONFIG", "OMP", "STP"}

const predictPreviewLimit = "Preview route, source-read from Forward, not run on a live Forward"

// predictBGPDiffs is verify-change view bgp_diffs: what a device's staged BGP advertisements change against the base snapshot, from the draft or one saved version.
func predictBGPDiffs(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.ChangeSetID == "" || in.Device == "" {
		return result.Result{}, fmt.Errorf("%w: view bgp_diffs needs change_set_id and device", ErrInvalidInput)
	}
	var diffs []forward.BGPAdvertisementDiff
	var err error
	which := "the uncommitted draft"
	if in.CommitID != "" {
		which = "version " + in.CommitID
		diffs, _, err = s.Client.Predict.CommittedBGPAdvertisementDiffs(ctx, in.NetworkID, in.ChangeSetID, in.CommitID, in.Device)
	} else {
		diffs, _, err = s.Client.Predict.BGPAdvertisementDiffs(ctx, in.NetworkID, in.ChangeSetID, in.Device)
	}
	if err != nil {
		if r, ok := denialResult(verifyChangeName, err, cx, "reading the BGP advertisement diffs"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	rows := make([]map[string]any, 0, len(diffs))
	for _, d := range diffs {
		rows = append(rows, map[string]any{"change": d.Change, "original": d.Original, "added": d.UpdateAdded, "removed": d.UpdateRemoved})
	}
	shown, om := result.Cap(rows, 100, "advertisement diffs", "the first 100 are listed")
	finding := fmt.Sprintf("%s in %s of change set %s stages %d BGP advertisement change(s)", in.Device, which, in.ChangeSetID, len(diffs))
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Omitted: om, NextActions: []string{"edit-change-set"},
		Limits:   []string{"zero diffs means no staged advertisement changes on this device in " + which + ", not that the device is unaffected; " + predictPreviewLimit},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictBGPDiffs", nil, map[string]any{"change_set_id": in.ChangeSetID, "device": in.Device, "source": which, "count": len(diffs), "diffs": shown}, finding)}})
}

// predictObjects is verify-change view objects: a firewall's address, service, application, url, user objects and zones as the change set holds them.
func predictObjects(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.ChangeSetID == "" || in.Device == "" || in.ObjectKind == "" {
		return result.Result{}, fmt.Errorf("%w: view objects needs change_set_id, device and object_kind (%s)", ErrInvalidInput, strings.Join(firewallObjectKinds, ", "))
	}
	ok := false
	for _, k := range firewallObjectKinds {
		ok = ok || k == in.ObjectKind
	}
	if !ok {
		return result.Result{}, fmt.Errorf("%w: object_kind is one of %s", ErrInvalidInput, strings.Join(firewallObjectKinds, ", "))
	}
	raw, _, err := s.Client.Predict.FirewallObjects(ctx, in.NetworkID, in.ChangeSetID, in.Device, forward.FirewallObjectKind(in.ObjectKind))
	if err != nil {
		if r, ok := denialResult(verifyChangeName, err, cx, "reading the firewall objects"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		items = []json.RawMessage{raw}
	}
	shown, om := result.Cap(items, 100, "objects", "the first 100 are listed")
	finding := fmt.Sprintf("%s on %s in change set %s: %d item(s)", in.ObjectKind, in.Device, in.ChangeSetID, len(items))
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Omitted: om, NextActions: []string{"edit-change-set"},
		Limits:   []string{"items are Forward's own JSON for the kind, unparsed; " + predictPreviewLimit},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictObjects", nil, map[string]any{"change_set_id": in.ChangeSetID, "device": in.Device, "object_kind": in.ObjectKind, "count": len(items), "items": shown}, finding)}})
}

// predictChecks is verify-change view checks: the NQE checks added to a change set.
func predictChecks(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.ChangeSetID == "" {
		return result.Result{}, fmt.Errorf("%w: view checks needs change_set_id", ErrInvalidInput)
	}
	checks, _, err := s.Client.Predict.ChangeSetNQEChecks(ctx, in.NetworkID, in.ChangeSetID)
	if err != nil {
		if r, ok := denialResult(verifyChangeName, err, cx, "reading the change set's NQE checks"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	rows := make([]map[string]any, 0, len(checks))
	for _, c := range checks {
		rows = append(rows, map[string]any{"query_id": c.QueryID, "present_in_head": c.PresentInHead, "restricted": c.Restricted, "repository": c.Repository})
	}
	shown, om := result.Cap(rows, 100, "NQE checks", "the first 100 are listed")
	finding := fmt.Sprintf("Change set %s has %d NQE check(s)", in.ChangeSetID, len(checks))
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Omitted: om, NextActions: []string{"edit-change-set"},
		Limits:   []string{"an empty list means no NQE check was added to this change set; the checks' results are in view describe. " + predictPreviewLimit},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictChecks", nil, map[string]any{"change_set_id": in.ChangeSetID, "count": len(checks), "checks": shown}, finding)}})
}

// predictCommands is verify-change view commands: the vendor OSes Predict models, and for one OS the commands it accepts per protocol (Forward's own rendering).
func predictCommands(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.OS == "" {
		oses, _, err := s.Client.Predict.PredictOSes(ctx)
		if err != nil {
			if r, ok := denialResult(verifyChangeName, err, cx, "reading the OSes Predict models"); ok {
				return r, nil
			}
			return result.Result{}, err
		}
		sort.Strings(oses)
		finding := fmt.Sprintf("Predict models %d vendor OS(es)", len(oses))
		return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{NextActions: []string{"verify-change"},
			Limits:   []string{"pass os (one of these) and protocol to read the commands Predict accepts; " + predictPreviewLimit},
			Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictOSes", nil, map[string]any{"oses": oses, "protocols": predictProtocols}, finding)}})
	}
	proto := strings.ToUpper(strings.TrimSpace(in.Protocol))
	if proto == "" {
		proto = "CONFIG"
	}
	known := false
	for _, p := range predictProtocols {
		known = known || p == proto
	}
	if !known {
		return result.Result{}, fmt.Errorf("%w: protocol is one of %s", ErrInvalidInput, strings.Join(predictProtocols, ", "))
	}
	text, _, err := s.Client.Predict.AllowedCommands(ctx, in.OS, forward.PredictProtocol(proto))
	if err != nil {
		if r, ok := denialResult(verifyChangeName, err, cx, "reading the commands Predict accepts"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	if strings.TrimSpace(text) == "" {
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("Predict lists no %s commands for %s", proto, in.OS), cx,
			[]string{"Forward answers an empty text for an OS Predict does not model or a protocol it has no commands for; run without os to list the modelled OSes"}, result.Options{})
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	shown, om := result.Cap(lines, 300, "command lines", "the first 300 lines are shown")
	finding := fmt.Sprintf("Predict accepts %d line(s) of %s commands for %s", len(lines), proto, in.OS)
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Omitted: om, NextActions: []string{"edit-change-set"},
		Limits:   []string{"Forward's rendering of its command tree; a command missing here is not modelled and edit-change-set will refuse it. " + predictPreviewLimit},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictCommands", nil, map[string]any{"os": in.OS, "protocol": proto, "lines_total": len(lines), "commands": shown}, finding)}})
}
