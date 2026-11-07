package fwd_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// From Forward 26.11 the snapshot listing leaves out predicted snapshots unless includePredicted=true. Finding a snapshot by id, the plain
// listing and the allow-list guard's placement must ask for them, so a predicted snapshot id still resolves (older builds ignore the flag).
func TestSnapshotListingsAskForPredictedSnapshots(t *testing.T) {
	listed := func(srv *fwdtest.Server) (n int, flagged bool) {
		flagged = true
		for _, c := range srv.Calls() {
			if c.Method == "GET" && c.Path == "/api/networks/n1/snapshots" {
				n++
				flagged = flagged && c.Query["includePredicted"] == "true"
			}
		}
		return n, flagged
	}
	r := map[string]fwdtest.Handler{"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))}
	s, srv := fwdtest.New(t, r)
	ctx := context.Background()
	if _, err := s.Snapshot(ctx, "n1", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshots(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if n, ok := listed(srv); n != 2 || !ok {
		t.Fatalf("Snapshot and Snapshots: %d listing(s), includePredicted=true on all: %v (%+v)", n, ok, srv.Calls())
	}

	gs, gsrv := guarded(t, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	if err := call(t, gs, "GET", "/api/snapshots/s1/metrics", ""); err != nil {
		t.Fatal(err)
	}
	if n, ok := listed(gsrv); n == 0 || !ok {
		t.Fatalf("the guard's placement: %d listing(s), includePredicted=true on all: %v", n, ok)
	}
}

// The outcome, against a fake that behaves like 26.11 (a predicted snapshot is listed only when asked for): a predicted id resolves.
func TestPredictedSnapshotIDResolvesWhenTheListingHidesPredictions(t *testing.T) {
	collected := fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")
	predicted := fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-02T00:00:00.000Z")
	predicted["parentSnapshotId"] = "s1"
	r := map[string]fwdtest.Handler{"GET /api/networks/n1/snapshots": func(req *http.Request, _ []byte) (int, any) {
		list := []map[string]any{collected}
		if req.URL.Query().Get("includePredicted") == "true" {
			list = append(list, predicted)
		}
		return 200, map[string]any{"snapshots": list}
	}}
	s, _ := fwdtest.New(t, r)
	sn, err := s.Snapshot(context.Background(), "n1", "p1")
	if err != nil || sn == nil || !sn.Predicted() {
		t.Fatalf("predicted snapshot p1 not found: %+v %v", sn, err)
	}
}
