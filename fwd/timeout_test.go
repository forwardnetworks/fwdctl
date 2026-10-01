package fwd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestForwardTimeoutEnvGovernsHowLongOneCallMayTake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	t.Setenv("FORWARD_URL", srv.URL)
	t.Setenv("FORWARD_USERNAME", "u")
	t.Setenv("FORWARD_PASSWORD", "p")

	t.Setenv("FORWARD_TIMEOUT", "100ms")
	s, err := NewSession(ConfigFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Networks(context.Background()); err == nil {
		t.Fatalf("a call slower than FORWARD_TIMEOUT must fail")
	}

	t.Setenv("FORWARD_TIMEOUT", "5s")
	s, err = NewSession(ConfigFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Networks(context.Background()); err != nil {
		t.Fatalf("a call within FORWARD_TIMEOUT must succeed: %v", err)
	}
}
