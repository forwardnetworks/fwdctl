package fwd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// writeCert saves the test server's certificate as a PEM file, the way an operator's CA bundle looks.
func writeCert(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(p, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A CA bundle lets an on-prem Forward with a private or self-signed certificate connect with verification ON: the bundle is the only thing trusted.
func TestACABundleLetsASelfSignedForwardConnectWithVerificationOn(t *testing.T) {
	srv := selfSigned(t)
	s, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s", CABundle: writeCert(t, srv)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Insecure() {
		t.Error("a CA bundle is not insecure mode")
	}
	if err := list(t, s); err != nil {
		t.Fatalf("the bundle's certificate was not trusted: %v", err)
	}
	// a bundle holding some other certificate authority does not cover this server
	s2, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s", CABundle: unrelatedCert(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := list(t, s2); err == nil {
		t.Error("a server the bundle does not cover was accepted")
	}
}

// unrelatedCert writes a PEM file holding a freshly made, self-signed certificate that signs nothing the test server uses.
func unrelatedCert(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "unrelated test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "other.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestACABundleWithInsecureIsRefusedAndABadBundleNamesTheFile(t *testing.T) {
	srv := selfSigned(t)
	good := writeCert(t, srv)
	if _, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s", Insecure: true, CABundle: good}); err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Errorf("insecure with a CA bundle: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	_ = os.WriteFile(empty, []byte("not a certificate\n"), 0o600)
	if _, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s", CABundle: empty}); err == nil || !strings.Contains(err.Error(), empty) {
		t.Errorf("a file with no certificate: %v", err)
	}
	if _, err := NewSession(Config{BaseURL: srv.URL, Username: "k", Password: "s", CABundle: "/no/such/file.pem"}); err == nil || !strings.Contains(err.Error(), "/no/such/file.pem") {
		t.Errorf("a missing file: %v", err)
	}
}

func TestConfigFromEnvReadsTheCABundle(t *testing.T) {
	t.Setenv("FORWARD_URL", "https://f")
	t.Setenv("FORWARD_USERNAME", "u")
	t.Setenv("FORWARD_PASSWORD", "p")
	t.Setenv("FORWARD_CA_BUNDLE", "/etc/fwd/ca.pem")
	if c := ConfigFromEnv(); c.CABundle != "/etc/fwd/ca.pem" {
		t.Errorf("config = %+v", c)
	}
}
