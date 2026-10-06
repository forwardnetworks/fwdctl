package skills_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/skills"
)

func impersonatedSession(t *testing.T) *fwd.Session {
	mux := http.NewServeMux()
	cookie := func(w http.ResponseWriter) {
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "s", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
	}
	mux.HandleFunc("/api/public/csrf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"headerName":"X-CSRF","parameterName":"_csrf","token":"t"}`))
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) { cookie(w); _, _ = w.Write([]byte(`{"location":"/"}`)) })
	mux.HandleFunc("/api/admin/impersonate", func(w http.ResponseWriter, r *http.Request) { cookie(w); _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/api/networks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"1","name":"n"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s, err := fwd.NewSession(fwd.Config{BaseURL: srv.URL, Username: "admin", Password: "pw", ImpersonateUserID: "2342", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestImpersonatedSessionRefusesApplyAllowsDryRunAndSaysItWasImpersonated(t *testing.T) {
	s := impersonatedSession(t)
	_, err := skills.Run(context.Background(), "edit-network", s, json.RawMessage(`{"action":"create","name":"x","apply":true}`))
	if !errors.Is(err, skills.ErrInvalidInput) || !strings.Contains(err.Error(), "impersonating user 2342") {
		t.Fatalf("apply must be refused: %v", err)
	}
	r, err := skills.Run(context.Background(), "inspect-networks", s, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "impersonating Forward user 2342") {
		t.Errorf("a read must say it was impersonated: %v", r.Limits)
	}
}
