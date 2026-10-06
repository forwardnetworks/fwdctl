package fwd_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// fakeForward answers the browser login, the impersonation and one read, and records what it saw.
func fakeForward(t *testing.T, allowImpersonation bool) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var seen []string
	mux := http.NewServeMux()
	setCookie := func(w http.ResponseWriter, v string) {
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: v, Path: "/"})
	}
	mux.HandleFunc("/api/public/csrf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"headerName":"X-CSRF","parameterName":"_csrf","token":"tok"}`))
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		setCookie(w, "admin-session")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"location":"/"}`))
	})
	mux.HandleFunc("/api/admin/impersonate", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "impersonate "+r.URL.Query().Get("user"))
		mu.Unlock()
		if !allowImpersonation {
			http.Error(w, `{"message":"forbidden"}`, 403)
			return
		}
		setCookie(w, "user-session")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/networks", func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie("SESSION")
		mu.Lock()
		seen = append(seen, "networks as "+c.Value+" basic="+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestImpersonatingSessionActsAsTheUserWithTheirSessionAndIsReadOnly(t *testing.T) {
	srv, seen := fakeForward(t, true)
	s, err := fwd.NewSession(fwd.Config{BaseURL: srv.URL, Username: "admin", Password: "pw", ImpersonateUserID: "2342", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if s.Impersonating() != "2342" {
		t.Errorf("Impersonating() = %q", s.Impersonating())
	}
	if _, _, err := s.Client.Networks.List(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*seen, "; ")
	if !strings.Contains(got, "impersonate 2342.full") || !strings.Contains(got, "networks as user-session basic=") || strings.Contains(got, "basic=Basic") {
		t.Errorf("saw: %s", got)
	}
	req, _ := s.Client.NewRequest(t.Context(), http.MethodDelete, "/api/networks/1", nil)
	if _, err := s.Client.Do(req, nil); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("a DELETE must be refused before it leaves the process: %v", err)
	}
}

func TestImpersonationRefusedByForwardIsAnErrorNotARunAsTheAdmin(t *testing.T) {
	srv, _ := fakeForward(t, false)
	if _, err := fwd.NewSession(fwd.Config{BaseURL: srv.URL, Username: "admin", Password: "pw", ImpersonateUserID: "2342", HTTPClient: srv.Client()}); err == nil || !strings.Contains(err.Error(), "FORWARD_IMPERSONATE") {
		t.Fatalf("a refused impersonation must fail the session, not fall back to the admin: %v", err)
	}
}
