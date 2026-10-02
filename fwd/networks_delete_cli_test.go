//go:build fwdctl_cli

package fwd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCLIBuildDeletesNetworksDirectly(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/api/networks/9" {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"9","name":"ws"}`))
	}))
	defer srv.Close()
	s, err := NewSession(Config{BaseURL: srv.URL, Username: "u", Password: "p", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNetwork(context.Background(), "9"); err != nil || hits.Load() != 1 {
		t.Fatalf("a CLI build deletes through the SDK: err=%v hits=%d", err, hits.Load())
	}
}
