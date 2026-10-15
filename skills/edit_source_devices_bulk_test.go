package skills_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// Many devices are changed in one call: definition.devices lists {name, ...fields}. One dry run shows each device's before and after (only the fields that change) and a count by collector; one apply patches
// them (a few at a time, retrying a transient failure) and reports a result per device. A single-device update's dry run shows only the fields that change, not blanks for the rest.
type bulkWorld struct {
	mu      sync.Mutex
	devs    map[string]map[string]any
	batches []map[string]any // each PatchMany request body
	flaky   int              // requests to fail with a TLS timeout first
	failFor string           // a collector id Forward refuses (the whole request fails)
}

// newBulkWorld serves n devices and Forward's single PatchMany request (PATCH classic-devices with {names, update}); a request is all or none.
func newBulkWorld(n int) (*bulkWorld, map[string]fwdtest.Handler) {
	w := &bulkWorld{devs: map[string]map[string]any{}}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("d%03d", i)
		w.devs[name] = map[string]any{"name": name, "host": "h" + name, "type": "cisco_ios_xe_ssh"}
	}
	return w, map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": func(*http.Request, []byte) (int, any) {
			w.mu.Lock()
			defer w.mu.Unlock()
			out := []any{}
			for _, d := range w.devs {
				out = append(out, d)
			}
			return 200, out
		},
		"PATCH /api/networks/n1/classic-devices": func(_ *http.Request, b []byte) (int, any) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.flaky > 0 {
				w.flaky--
				return 503, map[string]any{"message": "TLS handshake timeout"}
			}
			var req struct {
				Names  []string       `json:"names"`
				Update map[string]any `json:"update"`
			}
			_ = json.Unmarshal(b, &req)
			if w.failFor != "" && req.Update["collectorId"] == w.failFor {
				return 400, map[string]any{"message": "collector does not exist"}
			}
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			w.batches = append(w.batches, body)
			for _, n := range req.Names {
				for k, v := range req.Update {
					w.devs[n][k] = v
				}
			}
			return 204, nil
		},
	}
}

func TestBulkDeviceUpdateDryRunShowsOnlyChangedFieldsAndCountsByCollector(t *testing.T) {
	_, routes := newBulkWorld(6)
	in := `{"network_id":"n1","object":"classic_device","action":"update","definition":{"devices":[{"name":"d000","collectorId":"C1"},{"name":"d001","collectorId":"C1"},{"name":"d002","collectorId":"C2"}]}`
	r, srv := mustRun(t, "edit-source", routes, in+`}`)
	if writes(srv) != 0 || r.Status != result.OK || r.Changes[0].Applied {
		t.Fatalf("dry run: %s", jsonOf(r))
	}
	all := jsonOf(r)
	if !strings.Contains(all, `"C1":2`) || !strings.Contains(all, `"C2":1`) || !strings.Contains(all, `"requests":2`) {
		t.Errorf("a count by target collector, and two requests (one per distinct change): %s", all)
	}
	if strings.Contains(all, `"host":""`) || strings.Contains(all, `"name":""`) {
		t.Errorf("the after shows only what changes, not blanks: %s", all)
	}
}

// Devices with the same change go in one PatchMany request (one transaction: all or none); a transient failure retries the whole request.
func TestBulkDeviceUpdateGroupsIdenticalChangesIntoOneRequestAndRetriesTransientFailures(t *testing.T) {
	w, routes := newBulkWorld(60)
	w.flaky = 2
	var devs []string
	for i := 0; i < 60; i++ {
		devs = append(devs, fmt.Sprintf(`{"name":"d%03d","collectorId":"C%d"}`, i, i%2))
	}
	devs = append(devs, `{"name":"nope","collectorId":"C1"}`)
	r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","apply":true,"definition":{"devices":[`+strings.Join(devs, ",")+`]}}`)
	if r.Status != result.OK || len(w.batches) != 2 {
		t.Fatalf("two distinct changes are two requests of 30 names: %d batches, %s", len(w.batches), jsonOf(r))
	}
	for _, b := range w.batches {
		if len(b["names"].([]any)) != 30 {
			t.Errorf("a request carries every device with that change: %v", len(b["names"].([]any)))
		}
		if u := b["update"].(map[string]any); len(u) != 1 || u["collectorId"] == nil {
			t.Errorf("only the changed field is sent: %v", u)
		}
	}
	if !strings.Contains(jsonOf(r), "nope") {
		t.Errorf("an unknown device is named: %s", jsonOf(r))
	}
}

func TestBulkDeviceUpdateReportsARefusedRequestAsPartialAndAppliesTheOthers(t *testing.T) {
	w, routes := newBulkWorld(10)
	w.failFor = "C9"
	var devs []string
	for i := 0; i < 10; i++ {
		devs = append(devs, fmt.Sprintf(`{"name":"d%03d","collectorId":"C%d"}`, i, map[bool]int{true: 9, false: 1}[i < 4]))
	}
	r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","apply":true,"definition":{"devices":[`+strings.Join(devs, ",")+`]}}`)
	if r.Status != result.Failed || len(w.batches) != 1 || !strings.Contains(r.Finding, "were applied") {
		t.Fatalf("a refused request fails as a whole, the other applies, and the result says so: %s %s", r.Status, r.Finding)
	}
	if w.devs["d000"]["collectorId"] != nil {
		t.Errorf("a refused request changes none of its devices: %v", w.devs["d000"])
	}
}

func TestSingleDeviceUpdateAfterShowsOnlyTheChangedFields(t *testing.T) {
	_, routes := newBulkWorld(2)
	routes["PATCH /api/networks/n1/classic-devices/d000"] = fwdtest.Const(200, map[string]any{})
	r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","name":"d000","definition":{"collectorId":"C9"}}`)
	after, _ := json.Marshal(r.Changes[0].After)
	if strings.Contains(string(after), `"host"`) || !strings.Contains(string(after), "C9") {
		t.Errorf("after = %s", after)
	}
}

// clear resets a property to unset (an explicit null on the wire): the only way to unpin a device from a collector or remove a credential.
func TestDeviceUpdateClearSendsAnExplicitNullAndOnlyForAllowedProperties(t *testing.T) {
	w, routes := newBulkWorld(3)
	var sent string
	routes["PATCH /api/networks/n1/classic-devices/d000"] = func(_ *http.Request, b []byte) (int, any) { sent = string(b); return 200, map[string]any{} }
	mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","name":"d000","apply":true,"definition":{"clear":["collectorId"]}}`)
	if !strings.Contains(sent, `"collectorId":null`) {
		t.Errorf("clear sends null: %q", sent)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","name":"d000","apply":true,"definition":{"clear":["host"]}}`); err == nil {
		t.Errorf("a property that cannot be cleared must be refused")
	}
	w.devs["d001"]["collectorId"], w.devs["d002"]["collectorId"] = "C1", "C1" // pinned now, so clearing the pin is a change
	mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","apply":true,"definition":{"devices":[{"name":"d001","clear":["collectorId"]},{"name":"d002","clear":["collectorId"]}]}}`)
	if len(w.batches) != 1 || len(w.batches[0]["names"].([]any)) != 2 {
		t.Errorf("devices with the same clear go in one request: %v", w.batches)
	}
}
