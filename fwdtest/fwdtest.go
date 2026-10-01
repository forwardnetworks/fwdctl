// Package fwdtest is a recorded-response Forward for tests: a real HTTP server the real SDK talks to, so
// a skill is tested through the same request path it uses in production, with no network.
package fwdtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// Call is one request the fake received.
type Call struct {
	Method, Path string
	Query        map[string]string
	Body         map[string]any
}

// Handler answers one route. It returns an HTTP status and a body (a value to marshal, or []byte).
type Handler func(r *http.Request, body []byte) (int, any)

// Server is the fake.
type Server struct {
	*httptest.Server
	mu     sync.Mutex
	routes map[string]Handler
	calls  []Call
}

// Const answers a route with a fixed status and body.
func Const(status int, body any) Handler {
	return func(*http.Request, []byte) (int, any) { return status, body }
}

// Snapshots answers a snapshot listing, honouring the ?state= filter the way the server does, so a
// helper that forgets to ask for PROCESSED cannot pass by accident.
func Snapshots(list ...map[string]any) Handler {
	return func(r *http.Request, _ []byte) (int, any) {
		want := r.URL.Query().Get("state")
		out := []map[string]any{}
		for _, s := range list {
			if want == "" || s["state"] == want {
				out = append(out, s)
			}
		}
		return 200, map[string]any{"snapshots": out}
	}
}

// Snap builds a snapshot record.
func Snap(id, state, trigger, processed string) map[string]any {
	return map[string]any{"id": id, "state": state, "processingTrigger": trigger, "processedAt": processed, "createdAt": processed}
}

// New starts a fake with the given routes ("GET /api/networks/n1/snapshots") and returns a Session bound
// to it. An unrouted request answers 599 so a skill that makes an unplanned call fails loudly.
func New(t *testing.T, routes map[string]Handler) (*fwd.Session, *Server) {
	t.Helper()
	s := &Server{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	sess, err := fwd.NewSession(fwd.Config{BaseURL: s.URL, Username: "k", Password: "s", HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return sess, s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c := Call{Method: r.Method, Path: r.URL.Path, Query: map[string]string{}}
	for k, v := range r.URL.Query() {
		c.Query[k] = v[0]
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &c.Body)
	}
	s.mu.Lock()
	s.calls = append(s.calls, c)
	h := s.routes[r.Method+" "+r.URL.Path]
	s.mu.Unlock()
	if h == nil {
		http.Error(w, "no route in fake: "+r.Method+" "+r.URL.Path, 599)
		return
	}
	status, payload := h(r, body)
	if b, ok := payload.([]byte); ok {
		w.WriteHeader(status)
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Calls returns what the fake received.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Called reports whether a path was requested.
func (s *Server) Called(method, path string) bool {
	for _, c := range s.Calls() {
		if c.Method == method && c.Path == path {
			return true
		}
	}
	return false
}
