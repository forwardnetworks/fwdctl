package fwd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// selfSigned is a TLS server with a certificate no system root trusts, like a self-signed Forward.
func selfSigned(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"snapshots":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func list(t *testing.T, s *Session) error {
	t.Helper()
	_, _, err := s.Client.Snapshots.List(context.Background(), "n1", forward.SnapshotListOptions{})
	return err
}

func TestASelfSignedForwardIsRefusedByDefault(t *testing.T) {
	srv := selfSigned(t)
	s, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Insecure() {
		t.Fatal("a session is insecure by default")
	}
	if err := list(t, s); err == nil {
		t.Fatal("an unverifiable certificate was accepted with verification on")
	}
}

func TestInsecureAcceptsASelfSignedForwardAndSaysSo(t *testing.T) {
	srv := selfSigned(t)
	s, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Insecure() {
		t.Error("Insecure() is false for an insecure session")
	}
	if err := list(t, s); err != nil {
		t.Fatalf("insecure mode did not connect: %v", err)
	}
}

func TestConfigFromEnvReadsTheTLSSettings(t *testing.T) {
	t.Setenv("FORWARD_URL", "https://f")
	t.Setenv("FORWARD_USERNAME", "u")
	t.Setenv("FORWARD_PASSWORD", "p")
	t.Setenv("FORWARD_INSECURE", "TRUE")
	c := ConfigFromEnv()
	if !c.Insecure {
		t.Errorf("config = %+v", c)
	}
	t.Setenv("FORWARD_INSECURE", "no")
	if ConfigFromEnv().Insecure {
		t.Error(`"no" enabled insecure mode`)
	}
}
