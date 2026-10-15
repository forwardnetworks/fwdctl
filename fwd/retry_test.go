package fwd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// A write whose connection could not be obtained (dial error, TLS handshake timeout) sent nothing, so the session repeats it; before
// the SDK's v0.5.7 trace this came straight back on the first attempt.
func TestSessionRetriesAWriteThatNeverGotAConnection(t *testing.T) {
	var dials atomic.Int32
	hc := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("simulated handshake timeout")
	}}}
	s, err := NewSession(Config{BaseURL: "http://forward.invalid", Username: "u", Password: "p", HTTPClient: hc, RetryDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Client.DataFiles.ReplaceContent(context.Background(), "acl.csv", []byte("a\n"), nil)
	if err == nil {
		t.Fatal("expected the dial failure")
	}
	if got := dials.Load(); got != retryAttempts {
		t.Fatalf("a PATCH/POST that never obtained a connection is retried: %d dials, want %d", got, retryAttempts)
	}
	_ = forward.ErrRateLimited
}
