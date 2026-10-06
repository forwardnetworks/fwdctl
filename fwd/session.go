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
	// NQEMode and NQEWait set the Session fields of the same name (FORWARD_NQE_MODE and FORWARD_NQE_WAIT).
	NQEMode string
	NQEWait time.Duration
	// Insecure turns TLS certificate verification OFF, for self-signed installations. Otherwise the connection is
	// verified against the system trust store: a certificate is trusted by default or it is not. Insecure is never
	// the default, and every result records that it was used (see Session.Insecure).
	Insecure bool
	// ImpersonateUserID makes the session act as that Forward user through Forward's administrator impersonation (FORWARD_IMPERSONATE): the
	// login (Username and Password) must be an administrator that Forward allows to impersonate. The session is read-only (PUT, PATCH and
	// DELETE are refused at the transport, and the skills refuse apply) and every result says it was run as that user.
	ImpersonateUserID string
	// Hooks are extra SDK hooks the embedding program wants on every request (an audit logger, for example). They run after the
	// session's own recorder. fwdctl sets none.
	Hooks []forward.Hook
}

// ConfigFromEnv reads FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD, FORWARD_INSECURE and FORWARD_TIMEOUT.
func ConfigFromEnv() Config {
	c := Config{
		BaseURL:           strings.TrimSpace(os.Getenv("FORWARD_URL")),
		Username:          strings.TrimSpace(os.Getenv("FORWARD_USERNAME")),
		Password:          os.Getenv("FORWARD_PASSWORD"),
		Insecure:          truthy(os.Getenv("FORWARD_INSECURE")),
		ImpersonateUserID: strings.TrimSpace(os.Getenv("FORWARD_IMPERSONATE")),
	}
	// FORWARD_TIMEOUT (a Go duration, for example 600s) raises or lowers the time one HTTP call may take, response body included; the default is DefaultTimeout.
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("FORWARD_TIMEOUT"))); err == nil && d > 0 {
		c.Timeout = d
	}
	// FORWARD_NQE_MODE is auto (default), sync or async; FORWARD_NQE_WAIT bounds an asynchronous execution's wait.
	c.NQEMode = strings.ToLower(strings.TrimSpace(os.Getenv("FORWARD_NQE_MODE")))
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("FORWARD_NQE_WAIT"))); err == nil && d > 0 {
		c.NQEWait = d
	}
	return c
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

	impersonated string // the user id this session acts as (FORWARD_IMPERSONATE), or empty

	// NetworkDeleter, when set, performs every network deletion a skill asks for instead of the direct SDK call. A host that must route destructive calls through its own vetted
	// path sets it (and may return ErrDeletionRefused to forbid deletion); fwdctl leaves it nil and the SDK deletes directly.
	NetworkDeleter NetworkDeleter

	// OrganizationDeleter is the same seam for organizations: when set it performs every organization deletion edit-platform is asked for instead of the direct SDK call, and may return
	// ErrOrganizationDeletionRefused to forbid it. fwdctl leaves it nil and (built with -tags fwdctl_cli) deletes directly; a plain build has no direct delete and refuses.
	OrganizationDeleter OrganizationDeleter

	// NQEMode is how a query runs: "" or "auto" (synchronously; if the HTTP timeout cuts the request off, as an asynchronous execution), "sync" (never falls back) or
	// "async" (always through the execution API, for networks whose queries are known to be long). NQEWait bounds the wait for an asynchronous execution (default 10 minutes).
	NQEMode string
	NQEWait time.Duration

	mu       sync.Mutex
	ops      []result.Operation
	insecure bool
}

// NetworkDeleter deletes a network by id. It is the one seam through which this module deletes a network.
type NetworkDeleter func(ctx context.Context, networkID string) error

// OrganizationDeleter deletes an organization by id. It is the one seam through which this module deletes an organization.
type OrganizationDeleter func(ctx context.Context, orgID string) error

// ErrOrganizationDeletionRefused is what an OrganizationDeleter returns to say the host does not allow deleting organizations.
var ErrOrganizationDeletionRefused = errors.New("organization deletion is not allowed by the host running this skill")

// ErrDeletionRefused is what a NetworkDeleter returns to say the host does not allow deleting networks; skills report it as a refusal, not an error.
var ErrDeletionRefused = errors.New("network deletion is not allowed by the host running this skill")

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
	s := &Session{insecure: cfg.Insecure && cfg.HTTPClient == nil, NQEMode: cfg.NQEMode, NQEWait: cfg.NQEWait, impersonated: cfg.ImpersonateUserID}
	authMode := forward.AuthMode("")
	if cfg.ImpersonateUserID != "" {
		authMode = forward.AuthModeBrowser
		hc = readOnlyClient(hc)
	}
	c, err := forward.NewClient(forward.Config{
		AuthMode: authMode,
		BaseURL:  cfg.BaseURL, Username: cfg.Username, Password: cfg.Password, HTTPClient: hc,
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
	if cfg.ImpersonateUserID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := c.Browser.Login(ctx); err != nil {
			return nil, fmt.Errorf("FORWARD_IMPERSONATE: the administrator login failed: %w", err)
		}
		if _, _, err := c.Browser.Impersonate(ctx, cfg.ImpersonateUserID); err != nil {
			return nil, fmt.Errorf("FORWARD_IMPERSONATE: Forward refused to impersonate user %s (the login must be an administrator allowed to impersonate): %w", cfg.ImpersonateUserID, err)
		}
	}
	return s, nil
}

// Impersonating returns the user id this session acts as through administrator impersonation, or "" when it acts as its own login.
func (s *Session) Impersonating() string { return s.impersonated }

// readOnlyClient wraps hc so a PUT, PATCH or DELETE never leaves the process: an impersonated session reads. (Reads that POST, such
// as NQE and path searches, still go through.)
func readOnlyClient(hc *http.Client) *http.Client {
	c := *hc
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = readOnlyTransport{base}
	return &c
}

type readOnlyTransport struct{ next http.RoundTripper }

func (t readOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.Method {
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		return nil, fmt.Errorf("refused: this session is impersonating another user and is read-only (%s %s)", r.Method, r.URL.Path)
	}
	return t.next.RoundTrip(r)
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
// Status reports the HTTP status of a Forward error response, or 0 when err is not one.
func Status(err error) int {
	var er *forward.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		return er.Response.StatusCode
	}
	return 0
}

func NotAcceptable(err error) bool {
	var er *forward.ErrorResponse
	return errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == 406
}

// SetInsecureForTest marks the session insecure without changing its transport, so a test can check that
// results record it. It is not part of the API a harness should use.
func (s *Session) SetInsecureForTest() { s.insecure = true }
