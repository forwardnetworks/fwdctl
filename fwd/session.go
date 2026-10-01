// Package fwd is the thin layer between the skills and the Forward Go SDK.
//
// Skills reach Forward ONLY through the vendor SDK. This package adds what the skill contract needs
// on top of it and nothing else: retries off (a retried call turns an intermittent failure into a
// slower success and hides it; the harness owns retry policy), an operation log of every request,
// and helpers that refuse the traps that make a zero look like a pass.
package fwd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/result"
)

// DefaultTimeout bounds each HTTP call.
const DefaultTimeout = 120 * time.Second

// Config is how to reach a Forward instance.
type Config struct {
	BaseURL  string
	Username string // an API token's access key, or a login name
	Password string // an API token's secret, or a password
	// HTTPClient is optional. When set it is used as is and Insecure is ignored.
	HTTPClient *http.Client
	Timeout    time.Duration
	// Insecure turns TLS certificate verification OFF, for self-signed installations. Otherwise the connection is
	// verified against the system trust store: a certificate is trusted by default or it is not. Insecure is never
	// the default, and every result records that it was used (see Session.Insecure).
	Insecure bool
	// Hooks are extra SDK hooks the embedding program wants on every request (an audit logger, for example). They run after the
	// session's own recorder. fwdctl sets none.
	Hooks []forward.Hook
}

// ConfigFromEnv reads FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD.
func ConfigFromEnv() Config {
	return Config{
		BaseURL:  strings.TrimSpace(os.Getenv("FORWARD_URL")),
		Username: strings.TrimSpace(os.Getenv("FORWARD_USERNAME")),
		Password: os.Getenv("FORWARD_PASSWORD"),
		Insecure: truthy(os.Getenv("FORWARD_INSECURE")),
	}
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Session is one Forward client plus its operation log.
type Session struct {
	Client *forward.Client

	mu       sync.Mutex
	ops      []result.Operation
	insecure bool
}

// Insecure reports whether TLS verification is off for this session.
func (s *Session) Insecure() bool { return s.insecure }

// httpClient builds the client for a config: TLS verified against the system trust store, or not verified at all
// when Insecure is set.
func httpClient(cfg Config, timeout time.Duration) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.Insecure {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // explicit, opt-in, and recorded in every result
	}
	tr.TLSClientConfig = tlsCfg
	return &http.Client{Timeout: timeout, Transport: tr}, nil
}

// NewSession builds a session.
func NewSession(cfg Config) (*Session, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("FORWARD_URL is required (for example https://fwd.app)")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("FORWARD_USERNAME and FORWARD_PASSWORD are required")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	hc := cfg.HTTPClient
	if hc == nil {
		var err error
		if hc, err = httpClient(cfg, timeout); err != nil {
			return nil, err
		}
	}
	s := &Session{insecure: cfg.Insecure && cfg.HTTPClient == nil}
	c, err := forward.NewClient(forward.Config{
		BaseURL: cfg.BaseURL, Username: cfg.Username, Password: cfg.Password, HTTPClient: hc,
		UserAgent: "fwdctl",
		// Only a 429 or 503 is retried: the request was turned away before it ran, so repeating it cannot hide a
		// defect or replay a write. Every other failure comes back on the first attempt.
		Retry: forward.RetryPolicy{MaxAttempts: 3, Delay: time.Second, MaxDelay: 30 * time.Second, RefusedOnly: true, Jitter: 0.5},
		Hooks: append([]forward.Hook{s.record}, cfg.Hooks...),
	})
	if err != nil {
		return nil, err
	}
	s.Client = c
	return s, nil
}

// record is the SDK hook: one Operation per response (or transport error).
func (s *Session) record(_ context.Context, ev forward.Event) {
	if ev.Type != forward.EventResponse {
		return
	}
	name := ev.Operation
	if name == "" {
		name = ev.Method + " " + ev.Path
	}
	s.mu.Lock()
	s.ops = append(s.ops, result.Operation{Operation: name, Status: ev.StatusCode,
		DurationMS: float64(ev.Duration.Microseconds()) / 1000})
	s.mu.Unlock()
}

// Operations returns a copy of the log.
func (s *Session) Operations() []result.Operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]result.Operation(nil), s.ops...)
}

// Annotate attaches what a wrapper learned (row count, truncation) to the most recent operation.
func (s *Session) Annotate(rows *int, truncated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ops) == 0 {
		return
	}
	last := &s.ops[len(s.ops)-1]
	if rows != nil {
		last.Rows = rows
	}
	if truncated {
		last.Truncated = true
	}
}

func intp(n int) *int { return &n }

// forwardNotFound reports an HTTP 404 from Forward.
func forwardNotFound(err error) bool {
	var er *forward.ErrorResponse
	return errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == 404
}

// NotAcceptable reports Forward's 406. A text endpoint (the collection log) asked for text/plain answers its own error page in JSON,
// which Forward then refuses as not acceptable: the 406 stands in for the real error (for a log, no collection data), not a bad request.
func NotAcceptable(err error) bool {
	var er *forward.ErrorResponse
	return errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == 406
}

// SetInsecureForTest marks the session insecure without changing its transport, so a test can check that
// results record it. It is not part of the API a harness should use.
func (s *Session) SetInsecureForTest() { s.insecure = true }
