package fwd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// An embedding program's hooks (the server's delete audit) must see every request the session sends, including a tag removal.
func TestConfigHooksSeeEveryRequest(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	var seen []string
	s, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s", HTTPClient: srv.Client(),
		Hooks: []forward.Hook{func(_ context.Context, e forward.Event) {
			if e.Type == forward.EventResponse {
				seen = append(seen, e.Method+" "+e.Operation)
			}
		}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDeviceTags(context.Background(), "n1", []string{"r1"}, []string{"edge"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "POST DeviceTags.RemoveBatchFrom" {
		t.Fatalf("the hook saw %v", seen)
	}
	if len(s.Operations()) != 1 {
		t.Errorf("the session's own recorder must still run: %d operations", len(s.Operations()))
	}
}
