package fwd_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// guarded is a fake Forward with two networks: n1 (allowed) holding snapshot s1, and n2 (not allowed) holding snapshot s2.
func guarded(t *testing.T, adjust func(*fwd.Config)) (*fwd.Session, *fwdtest.Server) {
	t.Helper()
	ok := fwdtest.Const(200, map[string]any{})
	return fwdtest.NewWith(t, map[string]fwdtest.Handler{
		"GET /api/networks":                                        fwdtest.Const(200, []map[string]any{{"id": "n1", "name": "allowed"}, {"id": "n2", "name": "other"}}),
		"GET /api/networks/n1/snapshots":                           fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n2/snapshots":                           fwdtest.Snapshots(fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/change-sets":                         fwdtest.Const(200, []map[string]any{{"id": "cs1", "predictedSnapshots": []map[string]any{{"id": "p1", "networkId": "n1"}}}}),
		"GET /api/snapshots/p1/metrics":                            ok,
		"GET /api/networks/n1/change-sets/cs1/predicted-snapshots": fwdtest.Const(200, map[string]any{"snapshots": []map[string]any{{"id": "p2", "state": "PROCESSED"}}}),
		"GET /api/snapshots/p2/metrics":                            ok,
		"POST /api/networks/n1/change-sets/cs1":                    ok,
		"GET /api/snapshots/s1/metrics":                            ok,
		"GET /api/snapshots/s2/metrics":                            ok,
		"GET /api/diffs/s1/s2/devices":                             ok,
		"POST /api/nqe":                                            ok,
		"POST /api/networks/n1/paths-bulk":                         ok,
		"GET /api/users":                                           ok,
		"GET /api/version":                                         ok,
		"DELETE /api/networks/n1/snapshots/x":                      ok,
		"GET /api/integrations/servicenow":                         ok,
		"GET /api/nqe/repos/org/commits/head":                      ok,
		"PATCH /api/networks/n1":                                   ok,
		"POST /api/networks":                                       ok,
		"GET /api/users/current":                                   ok,
		"GET /api/nqe/schema":                                      ok,
		"GET /api/networks/n1/snapshots/other":                     ok,
	}, adjust)
}

func call(t *testing.T, s *fwd.Session, method, path, body string) error {
	t.Helper()
	ct := "application/json"
	if p, form, ok := strings.Cut(path, "|"); ok && form == "form" {
		path, ct = p, "application/x-www-form-urlencoded"
	}
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	var req *http.Request
	var err error
	if rd != nil {
		req, err = s.Client.NewRequest(context.Background(), method, path, rd)
	} else {
		req, err = s.Client.NewRequest(context.Background(), method, path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", ct)
	}
	_, err = s.Client.Do(req, nil)
	return err
}

func TestAllowNetworksRefusesOtherNetworksBeforeSending(t *testing.T) {
	s, srv := guarded(t, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	allowed := []struct{ method, path, body string }{
		{"GET", "/api/networks/n1/snapshots", ""},
		{"GET", "/api/snapshots/s1/metrics", ""},
		{"POST", "/api/nqe?snapshotId=s1", `{"query":"foreach d in network.devices select {n: d.name}"}`},
		{"POST", "/api/nqe?networkId=n1", `{"query":"x"}`},
		{"POST", "/api/networks/n1/paths-bulk", `{"queries":[{"srcIp":"192.0.2.1","dstIp":"192.0.2.2"}]}`},
		{"GET", "/api/version", ""},
		{"GET", "/api/users/current", ""},
		{"GET", "/api/nqe/schema", ""},
		{"GET", "/api/snapshots/p1/metrics", ""}, // a predicted snapshot of an allowed network's change set
		{"GET", "/api/snapshots/p2/metrics", ""}, // an older prediction, only in the change set's own list
	}
	for _, c := range allowed {
		if err := call(t, s, c.method, c.path, c.body); err != nil {
			t.Errorf("%s %s: refused or failed, want sent: %v", c.method, c.path, err)
		}
	}
	refused := []struct{ method, path, body, sent string }{
		{"GET", "/api/networks/n2/snapshots", "", "/api/networks/n2/snapshots"},
		{"GET", "/api/snapshots/s2/metrics", "", "/api/snapshots/s2/metrics"},
		{"GET", "/api/snapshots/s9/metrics", "", "/api/snapshots/s9/metrics"}, // Forward cannot place it: refused, not allowed
		{"GET", "/api/diffs/s1/s2/devices", "", "/api/diffs/s1/s2/devices"},
		{"POST", "/api/nqe?networkId=n2", `{"query":"x"}`, "/api/nqe"},
		{"POST", "/api/nqe", `{"query":"x"}`, "/api/nqe"},
		{"POST", "/api/networks/n1/paths-bulk", `{"queries":[{"snapshotId":"s2"}]}`, "/api/networks/n1/paths-bulk"},
		{"POST", "/api/networks/n1/paths-bulk", `{"networkId":"n2"}`, "/api/networks/n1/paths-bulk"},
		{"GET", "/api/networks/n1/snapshots/other?networkId=n2", "", "/api/networks/n1/snapshots/other"},
		{"GET", "/api/users", "", "/api/users"},
		{"GET", "/api/integrations/servicenow", "", "/api/integrations/servicenow"},
		{"GET", "/api/nqe/repos/org/commits/head", "", "/api/nqe/repos/org/commits/head"},
		{"POST", "/api/networks", `{"name":"new"}`, "/api/networks"},
		// other spellings and lists of the same ids: a change set copied into another network, a foreign base snapshot, id lists
		{"POST", "/api/networks/n1/change-sets/cs1?action=duplicate&targetNetworkId=n2", `{}`, "/api/networks/n1/change-sets/cs1"},
		{"POST", "/api/networks/n1/change-sets/cs1", `{"targetBaseSnapshotId":"s2"}`, "/api/networks/n1/change-sets/cs1"},
		{"POST", "/api/networks/n1/change-sets/cs1", `{"networkIds":["n1","n2"]}`, "/api/networks/n1/change-sets/cs1"},
		{"POST", "/api/networks/n1/change-sets/cs1", `{"x":{"snapshot_ids":["s1","s2"]}}`, "/api/networks/n1/change-sets/cs1"},
		{"GET", "/api/networks/n1/snapshots/other?network_id=n2", "", "/api/networks/n1/snapshots/other"},
		{"GET", "/api/networks/n1/snapshots/other?snapshotIds=s1,s2", "", "/api/networks/n1/snapshots/other"},
		// a form body is read by Forward as parameters, so it is checked as any other: it does not parse as JSON and is refused
		{"POST", "/api/networks/n1/paths-bulk|form", `snapshotId=s2`, "/api/networks/n1/paths-bulk"},
		// a body Go cannot read as JSON could still carry an id for Forward's parser
		{"POST", "/api/networks/n1/paths-bulk", `{"snapshotId":"s1"}x`, "/api/networks/n1/paths-bulk"},
	}
	for _, c := range refused {
		before := len(srv.Calls())
		err := call(t, s, c.method, c.path, c.body)
		if !errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("%s %s: err = %v, want a guard refusal", c.method, c.path, err)
			continue
		}
		if got := fwd.ErrorKind(err); got != fwd.KindRefused {
			t.Errorf("%s %s: ErrorKind = %q, want refused", c.method, c.path, got)
		}
		for _, sent := range srv.Calls()[before:] {
			if sent.Path == c.sent {
				t.Errorf("%s %s reached Forward; the guard must stop it before it is sent", c.method, c.path)
			}
		}
		if strings.Contains(err.Error(), "n2") && strings.Contains(c.path, "s2") && !strings.Contains(c.path, "n2") {
			t.Errorf("%s %s: the refusal names the network of a foreign snapshot: %v", c.method, c.path, err)
		}
	}
	// the guard never asked Forward anything about the other network or its snapshots
	for _, sent := range srv.Calls() {
		if strings.Contains(sent.Path, "/n2") || strings.Contains(sent.Path, "/s2") || strings.Contains(sent.Path, "/s9") {
			t.Errorf("the guard sent %s %s, a request about another network's data", sent.Method, sent.Path)
		}
	}
}

// A path the guard cannot read safely is refused, whatever it names.
func TestAllowNetworksRefusesUnreadablePaths(t *testing.T) {
	s, _ := guarded(t, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	for _, p := range []string{"/api/networks/n1/../n2/snapshots", "/api/networks/n1%2F..%2Fn2/snapshots", "/api/networks//n1"} {
		req, err := s.Client.NewRequest(context.Background(), "GET", "/api/version", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.URL.RawPath, req.URL.Path = p, p
		if u, perr := url.PathUnescape(p); perr == nil {
			req.URL.Path = u
		}
		if _, err := s.Client.Do(req, nil); !errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("%s: err = %v, want a refusal", p, err)
		}
	}
}

func TestAllowNetworksCannotBeCombinedWithImpersonation(t *testing.T) {
	_, err := fwd.NewSession(fwd.Config{BaseURL: "https://forward.example.invalid", Username: "k", Password: "s", AllowNetworks: []string{"n1"}, ImpersonateUserID: "u1"})
	if err == nil || !strings.Contains(err.Error(), "FORWARD_IMPERSONATE") {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowNetworksListsOnlyAllowedNetworks(t *testing.T) {
	s, _ := guarded(t, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	nets, _, err := s.Client.Networks.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 1 || nets[0].ID.String() != "n1" {
		t.Fatalf("network list = %+v, want only n1", nets)
	}
	if got := s.AllowedNetworks(); len(got) != 1 || got[0] != "n1" {
		t.Fatalf("AllowedNetworks = %v", got)
	}
}

func TestAllowNetworksSetButEmptyIsRefused(t *testing.T) {
	_, err := fwd.NewSession(fwd.Config{BaseURL: "https://forward.example.invalid", Username: "k", Password: "s", AllowNetworks: []string{" ", ""}})
	if err == nil || !strings.Contains(err.Error(), "allow-list") {
		t.Fatalf("err = %v, want the empty allow-list refused instead of allowing every network", err)
	}
}

func TestWithoutAllowListEveryNetworkIsReached(t *testing.T) {
	s, _ := guarded(t, nil)
	if err := call(t, s, "GET", "/api/networks/n2/snapshots", ""); err != nil {
		t.Fatal(err)
	}
	// an upload is streamed unread under the guard too: no ids to check
	sg, _ := guarded(t, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	req, _ := sg.Client.NewRequest(context.Background(), "POST", "/api/networks/n1/paths-bulk", strings.NewReader("PK\x03\x04 not json"))
	req.Header.Set("Content-Type", "application/zip")
	if _, err := sg.Client.Do(req, nil); err != nil {
		t.Fatalf("an upload body is not parsed: %v", err)
	}
	// an interface name with a slash, percent-encoded in a path, is a name, not a path step
	if err := call(t, sg, "GET", "/api/networks/n1/snapshots/"+url.PathEscape("Ethernet1/1"), ""); errors.Is(err, fwd.ErrGuardRefused) {
		t.Fatalf("an encoded slash in a name was refused: %v", err)
	}
	nets, _, err := s.Client.Networks.List(context.Background())
	if err != nil || len(nets) != 2 {
		t.Fatalf("nets = %v err = %v, want both networks", nets, err)
	}
}

func TestReadOnlyRefusesPutPatchDeleteBeforeSending(t *testing.T) {
	s, srv := guarded(t, func(c *fwd.Config) { c.ReadOnly = true })
	if !s.ReadOnly() {
		t.Fatal("ReadOnly() = false")
	}
	for _, c := range []struct{ method, path, body string }{{"DELETE", "/api/networks/n1/snapshots/x", ""}, {"PATCH", "/api/networks/n1", `{"name":"y"}`}} {
		err := call(t, s, c.method, c.path, c.body)
		if !errors.Is(err, fwd.ErrGuardRefused) || fwd.ErrorKind(err) != fwd.KindRefused {
			t.Errorf("%s: err = %v, want a refusal", c.method, err)
		}
		if srv.Called(c.method, c.path) {
			t.Errorf("%s %s was sent", c.method, c.path)
		}
	}
	if err := call(t, s, "POST", "/api/nqe?networkId=n1", `{"query":"x"}`); err != nil {
		t.Fatalf("a read that POSTs (NQE) must still go: %v", err)
	}
}

func TestErrorKindFromForwardStatus(t *testing.T) {
	s, _ := fwdtest.New(t, map[string]fwdtest.Handler{
		"GET /api/networks": fwdtest.Const(401, map[string]any{"message": "Bad credentials"}),
		"GET /api/version":  fwdtest.Const(404, map[string]any{"message": "no"}),
	})
	_, _, err := s.Client.Networks.List(context.Background())
	if got := fwd.ErrorKind(err); got != fwd.KindAuth {
		t.Fatalf("401: kind = %q (err %v), want auth", got, err)
	}
	if fwd.ErrorKind(nil) != "" {
		t.Fatal("nil error has a kind")
	}
	if got := fwd.ErrorKind(context.DeadlineExceeded); got != fwd.KindTimeout {
		t.Fatalf("deadline: kind = %q", got)
	}
}

// A refusal is permanent: the SDK does not retry it, even for a GET with the production retry delay.
func TestGuardRefusalIsNotRetried(t *testing.T) {
	s, _ := guarded(t, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"}; c.RetryDelay = time.Second })
	start := time.Now()
	err := call(t, s, "GET", "/api/networks/n2/snapshots", "")
	if !errors.Is(err, fwd.ErrGuardRefused) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("a refused GET took %s: it was retried", d)
	}
}

// The CLI commands and the configuration of one device in a change set are plain text. A session limited to a network must still be able to stage them, on exactly those two routes, with the
// network checked from the path; a text body anywhere else, a JSON body carrying a foreign id, and a foreign network stay refused.
func TestAllowNetworksLetsDeviceTextBodiesThroughOnlyOnTheTwoTextRoutes(t *testing.T) {
	ok := fwdtest.Const(204, nil)
	routes := map[string]fwdtest.Handler{
		"PUT /api/networks/n1/change-sets/cs1/draft/devices/r1/commands":  ok,
		"PUT /api/networks/n1/change-sets/cs1/draft/devices/r1/config":    ok,
		"PUT /api/networks/n2/change-sets/cs1/draft/devices/r1/commands":  ok,
		"POST /api/networks/n1/paths-bulk":                                ok,
		"PUT /api/networks/n1/change-sets/cs1/draft/devices/r1/other":     ok,
		"POST /api/networks/n1/change-sets/cs1/draft/devices/r1/commands": ok,
		"PUT /api/networks/n1/change-sets/cs1/draft/X/r1/commands":        ok,
		"PUT /api/networks/n1/X/cs1/draft/devices/r1/commands":            ok,
		"PUT /api/snapshots/s1/change-sets/cs1/draft/devices/r1/commands": ok,
		"GET /api/networks/n1/snapshots":                                  fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
	}
	s, srv := fwdtest.NewWith(t, routes, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	text := "interface Gi0/1\n shutdown\nip route 10.0.0.0 255.0.0.0 192.0.2.1\n"
	send := func(method, path, body, ct string) error {
		req, err := s.Client.NewRequest(context.Background(), method, path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		_, err = s.Client.Do(req, nil)
		return err
	}
	const plain = "text/plain;charset=UTF-8" // what the SDK sends on both routes
	for _, p := range []string{"/api/networks/n1/change-sets/cs1/draft/devices/r1/commands", "/api/networks/n1/change-sets/cs1/draft/devices/r1/config"} {
		if err := send("PUT", p, text, plain); err != nil {
			t.Errorf("a plain text body on %s must pass: %v", p, err)
		}
	}
	// JSON on a text route is still read and its ids checked: an allowed id passes
	if err := send("PUT", "/api/networks/n1/change-sets/cs1/draft/devices/r1/commands", `{"snapshotId":"s1"}`, "application/json"); err != nil {
		t.Errorf("JSON naming an allowed snapshot must pass: %v", err)
	}
	cmdPath := "/api/networks/n1/change-sets/cs1/draft/devices/r1/commands"
	for _, c := range []struct{ method, path, body, ct string }{
		{"PUT", "/api/networks/n2/change-sets/cs1/draft/devices/r1/commands", text, plain},       // another network: refused from the path
		{"POST", "/api/networks/n1/paths-bulk", "snapshotId=s2", plain},                          // a text body on another route
		{"PUT", "/api/networks/n1/change-sets/cs1/draft/devices/r1/other", text, plain},          // another leaf of the same shape
		{"POST", cmdPath, text, plain},                                                           // the same path with another method
		{"PUT", cmdPath, `{"snapshotId":"s2"}`, "application/json"},                              // JSON on the text route: ids checked
		{"PUT", cmdPath, `{"snapshotId":"s2"}`, plain},                                           // JSON-shaped text: read as JSON too
		{"PUT", "/api/networks/n1/change-sets/cs1/notdraft/devices/r1/commands", text, plain},    // the middle of the path matters
		{"PUT", "/api/networks/n1/change-sets/cs1/draft/X/r1/commands", text, plain},             // devices segment
		{"PUT", "/api/networks/n1/X/cs1/draft/devices/r1/commands", text, plain},                 // change-sets segment
		{"PUT", "/api/snapshots/s1/change-sets/cs1/draft/devices/r1/commands", text, plain},      // networks segment
		{"PUT", "/api/networks/n1/change-sets/cs1/draft/devices", text, plain},                   // a shorter path
		{"PUT", "/api/networks/n1/change-sets/cs1/draft/devices/r1/commands/extra", text, plain}, // a longer one
		{"PUT", cmdPath, "networkId=n2&snapshotId=s2", "application/x-www-form-urlencoded"},      // a form: Forward reads parameters
		{"PUT", cmdPath, "networkId=n2&snapshotId=s2", "text/html"},                              // another text type is not plain text
		{"PUT", cmdPath, "networkId=n2&snapshotId=s2", ""},                                       // no type: not claimed to be text
		{"PUT", cmdPath, "\ufeff{\"snapshotId\":\"s2\"}", "application/json"},                    // a BOM: Go refuses it, a lenient parser may not
		{"PUT", cmdPath, `{"snapshotId":"s2"} x`, "application/json"},                            // trailing data
	} {
		before := len(srv.Calls())
		err := send(c.method, c.path, c.body, c.ct)
		if !errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("%s %s (%s): err = %v, want a guard refusal", c.method, c.path, c.ct, err)
		}
		for _, sent := range srv.Calls()[before:] { // the guard may read the allowed network's snapshot list to judge an id; the refused request itself must never reach Forward
			if sent.Method == c.method && sent.Path == c.path {
				t.Errorf("%s %s: a refused request was sent", c.method, c.path)
			}
		}
	}
}

// A CVE's detection logic belongs to the CVE index, not to a network, so it has no network in its path. A network-limited session may read exactly that one route.
func TestAllowNetworksLetsTheCVEDetectionLogicRouteThroughAndNothingElseUnderCVEs(t *testing.T) {
	ok := fwdtest.Const(200, map[string]any{})
	routes := map[string]fwdtest.Handler{
		"GET /api/cves/CVE-2024-0001/detection-logic":    ok,
		"POST /api/cves/CVE-2024-0001/detection-logic":   ok,
		"GET /api/cves/CVE-2024-0001":                    ok,
		"GET /api/cves/CVE-2024-0001/detection-logic/x":  ok,
		"GET /api/cves/not-a-cve/detection-logic":        ok,
		"GET /api/cves/CVE-2024-0001/other":              ok,
		"GET /api/cves":                                  ok,
		"PUT /api/cves/CVE-2024-0001/detection-logic":    ok,
		"DELETE /api/cves/CVE-2024-0001/detection-logic": ok,
	}
	s, srv := fwdtest.NewWith(t, routes, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	send := func(method, path string) error {
		req, err := s.Client.NewRequest(context.Background(), method, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Client.Do(req, nil)
		return err
	}
	if err := send("GET", "/api/cves/CVE-2024-0001/detection-logic"); err != nil {
		t.Errorf("the detection-logic route must pass: %v", err)
	}
	if err := send("GET", "/api/cves/cve-2024-0001/detection-logic"); err != nil && errors.Is(err, fwd.ErrGuardRefused) {
		t.Errorf("a lower-case id is the same route: %v", err)
	}
	// the query string and a body are still checked on the allowed route
	for _, p := range []string{"/api/cves/CVE-2024-0001/detection-logic?networkId=n2", "/api/cves/CVE-2024-0001/detection-logic?snapshotId=s2", "/api/cves/CVE-2024-0001/detection-logic?target_network_id=n2"} {
		before := len(srv.Calls())
		if err := send("GET", p); !errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("%s: err = %v, want a guard refusal", p, err)
		}
		for _, c := range srv.Calls()[before:] {
			if c.Path == "/api/cves/CVE-2024-0001/detection-logic" && (c.Query["networkId"] != "" || c.Query["snapshotId"] != "" || c.Query["target_network_id"] != "") {
				t.Errorf("%s: a refused request was sent", p)
			}
		}
	}
	for _, p := range []string{"/api/cves/CVE-2024-0001/detection-logic/", "/api/cves/%43VE-2024-0001/detection-logic"} {
		if err := send("GET", p); errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("%s is the same route and must not be refused by the guard: %v", p, err)
		}
	}
	if req, err := s.Client.NewRequest(context.Background(), "GET", "/api/cves/CVE-2024-0001/detection-logic", strings.NewReader(`{"networkId":"n2"}`)); err == nil {
		req.Header.Set("Content-Type", "application/json")
		if _, err := s.Client.Do(req, nil); !errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("a body naming another network is refused on the allowed route: %v", err)
		}
	}
	if err := send("HEAD", "/api/cves/CVE-2024-0001/detection-logic"); !errors.Is(err, fwd.ErrGuardRefused) {
		t.Errorf("HEAD is refused: %v", err)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/cves/CVE-2024-0001/detection-logic"},
		{"PUT", "/api/cves/CVE-2024-0001/detection-logic"},
		{"DELETE", "/api/cves/CVE-2024-0001/detection-logic"},
		{"GET", "/api/cves/CVE-2024-0001"},
		{"GET", "/api/cves/CVE-2024-0001/detection-logic/x"},
		{"GET", "/api/cves/not-a-cve/detection-logic"},
		{"GET", "/api/cves/CVE-2024-0001/other"},
		{"GET", "/api/cves"},
	} {
		before := len(srv.Calls())
		if err := send(c.method, c.path); !errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("%s %s: err = %v, want a guard refusal", c.method, c.path, err)
		}
		if len(srv.Calls()) != before {
			t.Errorf("%s %s: a refused request was sent", c.method, c.path)
		}
	}
}

// A network-limited session may read the value of one Predict flag, and nothing else under /api/config: the whole map also holds the organization's IP allow-list and password policy.
func TestAllowNetworksReadsOnlyThePredictFlagsOneAtATime(t *testing.T) {
	ok := fwdtest.Const(200, map[string]any{"predict_model_config": true})
	routes := map[string]fwdtest.Handler{
		"GET /api/config":                         ok,
		"GET /api/config/ALLOWED_IPS":             ok,
		"GET /api/config/predict_model_config":    ok,
		"PUT /api/config/PREDICT_MODEL_CONFIG":    ok,
		"DELETE /api/config/PREDICT_MODEL_CONFIG": ok,
		"POST /api/config/PREDICT_MODEL_CONFIG":   ok,
		"GET /api/config/PREDICT_MODEL_CONFIG/x":  ok,
		"GET /api/orgs/7/config":                  ok,
		"GET /api/orgs/7/config/PREDICT_MODELING": ok,
		"GET /api/global-config":                  ok,
		"GET /api/config/PREDICT_MODEL_CONFIG":    ok,
	}
	nine := []string{"PREDICT_MODELING", "PREDICT_MODEL_BGP", "PREDICT_MODEL_OSPF", "PREDICT_MODEL_CONFIG", "PREDICT_MODEL_CROSS_PROTOCOL", "PREDICT_MODEL_ISIS", "PREDICT_MODEL_OMP", "PREDICT_MODEL_EXT_ADV", "FIREWALL_PREDICT"}
	for _, n := range nine {
		routes["GET /api/config/"+n] = ok
	}
	for _, p := range []string{"GET /api/config/PREDICT_MODELING;x=1", "GET /api/config/PREDICT_MODELING.json", "HEAD /api/config/PREDICT_MODELING", "GET /api/config/PREDICT_MODELING%2Fx"} {
		routes[p] = ok
	}
	s, srv := fwdtest.NewWith(t, routes, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	send := func(method, path string) error {
		req, err := s.Client.NewRequest(context.Background(), method, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Client.Do(req, nil)
		return err
	}
	for _, n := range nine {
		if err := send("GET", "/api/config/"+n); err != nil {
			t.Errorf("reading %s must pass: %v", n, err)
		}
	}
	if got := fwd.PredictFlagProperties(); strings.Join(got, ",") != strings.Join(nine, ",") {
		t.Errorf("the allow list is exactly the nine Predict flags: %v", got)
	}
	fwd.PredictFlagProperties()[0] = "ALLOWED_IPS" // a returned copy: changing it must not widen the guard
	if err := send("GET", "/api/config/ALLOWED_IPS"); !errors.Is(err, fwd.ErrGuardRefused) {
		t.Errorf("the allow list cannot be changed from outside: %v", err)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/config"},
		{"GET", "/api/config?filter=OFF"},
		{"GET", "/api/config/ALLOWED_IPS"},
		{"GET", "/api/config/predict_model_config"}, // the SDK sends the upper-case enum name; anything else is not the route
		{"GET", "/api/config/PREDICT_MODEL_CONFIG?x=1"},
		{"GET", "/api/config/PREDICT_MODEL_CONFIG/x"},
		{"GET", "/api/config/PREDICT_MODELING;x=1"},
		{"GET", "/api/config/PREDICT_MODELING.json"},
		{"GET", "/api/config/PREDICT_MODELING%2Fx"},
		{"HEAD", "/api/config/PREDICT_MODELING"},
		{"PUT", "/api/config/PREDICT_MODEL_CONFIG"},
		{"POST", "/api/config/PREDICT_MODEL_CONFIG"},
		{"DELETE", "/api/config/PREDICT_MODEL_CONFIG"},
		{"GET", "/api/orgs/7/config"},
		{"GET", "/api/orgs/7/config/PREDICT_MODELING"},
		{"GET", "/api/global-config"},
	} {
		before := len(srv.Calls())
		if err := send(c.method, c.path); !errors.Is(err, fwd.ErrGuardRefused) {
			t.Errorf("%s %s: err = %v, want a guard refusal", c.method, c.path, err)
		}
		if len(srv.Calls()) != before {
			t.Errorf("%s %s: a refused request was sent", c.method, c.path)
		}
	}
}
