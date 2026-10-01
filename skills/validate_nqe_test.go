package skills_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const nqePath = "POST /api/nqe"

func nqeRun(t *testing.T, snaps fwdtest.Handler, nqe fwdtest.Handler, extra string) (result.Result, *fwdtest.Server) {
	t.Helper()
	in := `{"network_id":"n1","query":"foreach d in network.devices select {Name: d.name}"` + extra + `}`
	return mustRun(t, "validate-nqe-query", map[string]fwdtest.Handler{snapsPath: snaps, nqePath: nqe}, in)
}

func TestValidateNQERowsAreOKWithABoundedSample(t *testing.T) {
	rows := []map[string]any{{"Name": "r0"}, {"Name": "r1"}, {"Name": "r2"}}
	r, srv := nqeRun(t, ready("s1"), fwdtest.Const(200, map[string]any{"items": rows, "totalNumItems": 40}), `,"sample_rows":2`)
	if r.Status != result.OK || r.Confidence != result.Deterministic {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
	d := r.Evidence[0].Detail
	if d["rows"] != int64(40) && d["rows"] != float64(40) && d["rows"] != 40 {
		t.Errorf("rows = %v (%T)", d["rows"], d["rows"])
	}
	if got := len(d["sample"].([]map[string]any)); got != 2 {
		t.Errorf("sample = %d rows", got)
	}
	var sent map[string]any
	for _, c := range srv.Calls() {
		if c.Method == "POST" && c.Path == "/api/nqe" {
			sent = c.Body
		}
	}
	if opts := sent["queryOptions"].(map[string]any); opts["limit"] != float64(2) {
		t.Errorf("limit sent = %v", opts["limit"])
	}
	if _, present := sent["parameters"]; present {
		t.Errorf("a parameter-less run must not send a parameters key (Forward rejects null and {}): %v", sent)
	}
}

func TestValidateNQECompileErrorIsAFailureWithItsPosition(t *testing.T) {
	body := map[string]any{"apiUrl": "/api/nqe", "httpMethod": "POST", "message": "Query failed to compile",
		"errors": []map[string]any{{"message": "Unknown alternative PALO_ALTO",
			"location": map[string]any{"start": map[string]any{"line": 3, "character": 12}, "end": map[string]any{"line": 3, "character": 28}}}}}
	r, _ := nqeRun(t, ready("s1"), fwdtest.Const(400, body), "")
	if r.Status != result.Failed || !strings.Contains(r.Finding, "Unknown alternative PALO_ALTO") ||
		!strings.Contains(r.Finding, "line 3, column 12 (0-based)") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestValidateNQEZeroRowsIsUnknownNeverOK(t *testing.T) {
	r, _ := nqeRun(t, ready("s1"), fwdtest.Const(200, map[string]any{"items": []any{}, "totalNumItems": 0}), "")
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "zero rows") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestValidateNQEUnprocessedSnapshotRunsNothing(t *testing.T) {
	snaps := fwdtest.Snapshots(fwdtest.Snap("s1", "UNPROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
	r, srv := nqeRun(t, snaps, fwdtest.Const(200, map[string]any{"items": []any{}}), "")
	if r.Status != result.Unknown || srv.Called("POST", "/api/nqe") {
		t.Fatalf("status %s, ran=%v", r.Status, srv.Called("POST", "/api/nqe"))
	}
}

func TestValidateNQEA409MidFlightIsUnknownNotAFailureOfTheQuery(t *testing.T) {
	r, _ := nqeRun(t, ready("s1"), fwdtest.Const(409, map[string]any{"message": "snapshot still processing", "reason": "SNAPSHOT_UNAVAILABLE"}), "")
	if r.Status != result.Unknown {
		t.Fatalf("status %s: %s", r.Status, r.Finding)
	}
}

func TestValidateNQEPredictedSnapshotIsFlagged(t *testing.T) {
	snaps := fwdtest.Snapshots(fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-05T00:00:00.000Z"))
	r, _ := nqeRun(t, snaps, fwdtest.Const(200, map[string]any{"items": []map[string]any{{"a": 1}}, "totalNumItems": 1}), `,"snapshot_id":"p1"`)
	if !strings.Contains(strings.Join(r.Limits, " "), "predicted") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestValidateNQEParametersAreSentByName(t *testing.T) {
	var body map[string]any
	h := func(_ *http.Request, b []byte) (int, any) {
		_ = json.Unmarshal(b, &body)
		return 200, map[string]any{"items": []map[string]any{{"a": 1}}, "totalNumItems": 1}
	}
	_, _ = nqeRun(t, ready("s1"), h, `,"parameters":{"days":7}`)
	if p, _ := body["parameters"].(map[string]any); p["days"] != float64(7) {
		t.Errorf("parameters = %v", body["parameters"])
	}
	_ = io.EOF
}

func TestValidateNQEAFailedToProcess400IsUnknownButAnUnrelated400IsNotSwallowed(t *testing.T) {
	r, _ := nqeRun(t, ready("s1"), fwdtest.Const(400, map[string]any{"message": "The snapshot you are attempting to access failed to process successfully"}), "")
	if r.Status != result.Unknown {
		t.Fatalf("failed-to-process: status %s", r.Status)
	}
	_, _, err := runSkill(t, "validate-nqe-query", map[string]fwdtest.Handler{snapsPath: ready("s1"),
		nqePath: fwdtest.Const(400, map[string]any{"message": "malformed request body"})},
		`{"network_id":"n1","query":"select 1"}`)
	if err == nil {
		t.Fatal("an unrelated 400 was swallowed as a result")
	}
}

func TestValidateNQENoQualifyingSnapshotIsUnknownWhetherOrNotASnapshotExists(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"a snapshot exists but is unprocessed": {"message": "no qualifying snapshot", "reason": "NO_QUALIFYING_SNAPSHOT", "latestSnapshotId": "9", "latestSnapshotState": "PROCESSING"},
		"the network has no snapshot":          {"message": "no qualifying snapshot", "reason": "NO_QUALIFYING_SNAPSHOT"},
	} {
		r, _ := nqeRun(t, ready("s1"), fwdtest.Const(409, body), "")
		if r.Status != result.Unknown {
			t.Errorf("%s: status %s", name, r.Status)
		}
	}
}

func TestValidateNQECanceledOrTimedOutProcessingIsUnknown(t *testing.T) {
	r, _ := nqeRun(t, ready("s1"), fwdtest.Const(400, map[string]any{"message": "Processing was canceled for Snapshot 12"}), "")
	if r.Status != result.Unknown {
		t.Errorf("status %s: %s", r.Status, r.Finding)
	}
}

func TestValidateNQEAnInventedEnumValueGetsTheRealOnesInTheFinding(t *testing.T) {
	needCorpora(t)
	body := map[string]any{"apiUrl": "/api/nqe", "httpMethod": "POST", "message": "Query failed to compile",
		"errors": []map[string]any{{"message": "Unknown alternative PALO_ALTO",
			"location": map[string]any{"start": map[string]any{"line": 0, "character": 56}, "end": map[string]any{"line": 0, "character": 72}}}}}
	in := `{"network_id":"n1","query":"foreach d in network.devices where d.platform.vendor == Vendor.PALO_ALTO select d.name"}`
	r, _, err := runSkill(t, "validate-nqe-query", map[string]fwdtest.Handler{snapsPath: ready("s1"), nqePath: fwdtest.Const(400, body)}, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != result.Failed || !strings.Contains(r.Finding, "did you mean Vendor.PALO_ALTO_NETWORKS?") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
	if _, ok := r.Evidence[0].Detail["schema_hints"]; !ok {
		t.Error("the hints are not in the evidence")
	}
}

func TestValidateNQEADiscouragedFilterThatRunsIsALimitNotAFailure(t *testing.T) {
	needCorpora(t)
	in := `{"network_id":"n1","query":"foreach d in network.devices where d.platform.deviceType == DeviceType.ROUTER select {n: d.name}"}`
	r, _, err := runSkill(t, "validate-nqe-query", map[string]fwdtest.Handler{snapsPath: ready("s1"),
		nqePath: fwdtest.Const(200, map[string]any{"items": []map[string]any{{"n": "r1"}}, "totalNumItems": 1})}, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "vendor-assigned") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestValidateNQEPagesALargeResultWithOffset(t *testing.T) {
	seen := map[string]any{}
	nqe := func(_ *http.Request, b []byte) (int, any) {
		_ = json.Unmarshal(b, &seen)
		return 200, map[string]any{"items": []any{map[string]any{"a": 1}, map[string]any{"a": 2}}, "totalNumItems": 50}
	}
	r, _ := mustRun(t, "validate-nqe-query", map[string]fwdtest.Handler{snapsPath: ready("s1"), nqePath: nqe},
		`{"network_id":"n1","query":"foreach d in network.devices select {a: 1}","sample_rows":2,"offset":10}`)
	opts, _ := seen["queryOptions"].(map[string]any)
	if opts["offset"] != float64(10) || opts["limit"] != float64(2) {
		t.Errorf("paging options not sent: %v", seen)
	}
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, "|"), "Page with offset=12") {
		t.Errorf("%s %v", r.Status, r.Limits)
	}
}

func TestValidateChecksUnsavedSyntheticRowsWhenAKindIsGiven(t *testing.T) {
	needCorpora(t)
	// the query runs, but its rows are not internet connections: the kind check makes that a failure, and nothing is saved
	r, srv := nqeRun(t, ready("s1"), fwdtest.Const(200, map[string]any{"items": []map[string]any{{"x": 1}}, "totalNumItems": 1}), `,"synthetic_kind":"internet"`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "not valid internet connections") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	for _, c := range srv.Calls() {
		if c.Method != "POST" || c.Path != "/api/nqe" {
			if c.Method != "GET" {
				t.Errorf("an unsaved preview must not write: %s %s", c.Method, c.Path)
			}
		}
	}
}
