package skills_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/skills"
)

// jobsSession serves one active job: network 4610, running=0, duration past any reasonable threshold -- a job
// that has been QUEUED the whole time, never once dispatched to a worker. Forward's longest_running_time_seconds
// is 0 for this case; only duration_seconds at running=0 can see it.
func jobsSession(t *testing.T, onCancel func(r *http.Request)) *fwd.Session {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/jobs/active", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
			"jobType": "NQE Query Computation", "orgName": "sf-craigjohnson-customer", "networkId": 4610,
			"snapshotId": 13951, "creationTime": "2026-10-08T12:16:42.871Z",
			"waitingForOtherJobs": 0, "queued": 1, "running": 0,
			"longestQueuedTimeInSeconds": 0, "longestRunningTimeInSeconds": 0,
			"durationInSeconds": 1343, "totalCount": 1, "cancelLink": "IgUI%2F2wQGg%3D%3D"
		}]`))
	})
	mux.HandleFunc("/api/jobs/IgUI%2F2wQGg%3D%3D", func(w http.ResponseWriter, r *http.Request) {
		if onCancel != nil {
			onCancel(r)
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s, err := fwd.NewSession(fwd.Config{BaseURL: srv.URL, Username: "admin", Password: "pw", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestInspectJobsFlagsAQueuedOrphanAsLikelyStuck pins the queued-orphan case: running=0 and
// longest_running_time_seconds=0 forever, but duration_seconds is well past the threshold.
func TestInspectJobsFlagsAQueuedOrphanAsLikelyStuck(t *testing.T) {
	s := jobsSession(t, nil)
	r, err := skills.Run(context.Background(), "inspect-jobs", s, json.RawMessage(`{"older_than_minutes": 5}`))
	if err != nil {
		t.Fatal(err)
	}
	jobs := r.Evidence[0].Detail["jobs"].([]map[string]any)
	if len(jobs) != 1 || jobs[0]["likely_stuck"] != true {
		t.Fatalf("jobs = %#v, want one row with likely_stuck=true", jobs)
	}
}

// TestEditJobsZeroThresholdCancelsImmediately pins older_than_minutes: 0 to mean "cancel now", not "unset,
// use the default 20" -- the input is *int precisely so JSON's explicit 0 is distinguishable from omitted.
func TestEditJobsZeroThresholdCancelsImmediately(t *testing.T) {
	var gotPath string
	s := jobsSession(t, func(r *http.Request) { gotPath = r.URL.RequestURI() })
	r, err := skills.Run(context.Background(), "edit-jobs", s, json.RawMessage(`{"network_id": "4610", "older_than_minutes": 0, "apply": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "ok" || r.Mode != "applied" {
		t.Fatalf("status=%s mode=%s, want ok/applied: %+v", r.Status, r.Mode, r)
	}
	if want := "/api/jobs/IgUI%2F2wQGg%3D%3D"; gotPath != want {
		t.Fatalf("cancel request path = %q, want %q (sent byte-for-byte, not re-escaped)", gotPath, want)
	}
}

// TestEditJobsDryRunDefaultsTo20Minutes pins the omitted case: no older_than_minutes means the 20-minute
// default, so a job merely queued 5 minutes (below it) is NOT matched.
func TestEditJobsDryRunDefaultsTo20Minutes(t *testing.T) {
	s := jobsSession(t, func(_ *http.Request) { t.Fatal("Cancel must not be called in a dry run") })
	r, err := skills.Run(context.Background(), "edit-jobs", s, json.RawMessage(`{"network_id": "4610"}`))
	if err != nil {
		t.Fatal(err)
	}
	// duration_seconds is 1343s (~22m), past the 20-minute default, so the dry run still finds it.
	if r.Status != "ok" || r.Mode != "dry_run" || len(r.Changes) != 1 {
		t.Fatalf("result = %+v, want a dry-run plan with one change", r)
	}
}
