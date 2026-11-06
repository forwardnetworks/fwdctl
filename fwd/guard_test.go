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
