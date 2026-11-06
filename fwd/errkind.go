package fwd

import (
	"context"
	"crypto/x509"
	"errors"
	"net"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Error kinds, the machine-readable reason a run could not complete (Result.ErrorKind). A caller branches on these instead of parsing the message.
const (
	KindRefused      = "refused"       // this session's guard stopped it before it was sent (read-only, allow-list, impersonation)
	KindInvalidInput = "invalid_input" // the inputs were rejected before anything was sent
	KindAuth         = "auth"          // Forward answered 401: the login is wrong or expired
	KindForbidden    = "forbidden"     // Forward answered 403: the login lacks the permission or licence
	KindNotFound     = "not_found"     // Forward answered 404: the network, snapshot or object does not exist for this login
	KindRateLimited  = "rate_limited"  // Forward answered 429 or locked the login out for a while
	KindTimeout      = "timeout"       // the call or the run ran out of time
	KindTLS          = "tls"           // the certificate could not be verified
	KindConnection   = "connection"    // Forward could not be reached (DNS, refused connection)
	KindUpstream     = "upstream"      // Forward answered 5xx or something it should not have
	KindRejected     = "rejected"      // Forward answered another 4xx (it refused the request as sent)
	KindOther        = "other"
)

// ErrorKind classifies err for Result.ErrorKind. It returns "" for a nil error.
func ErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrGuardRefused) {
		return KindRefused
	}
	if errors.Is(err, forward.ErrRateLimited) {
		return KindRateLimited
	}
	switch st := Status(err); {
	case st == 401:
		return KindAuth
	case st == 403:
		return KindForbidden
	case st == 404:
		return KindNotFound
	case st == 408 || st == 504:
		return KindTimeout
	case st == 429:
		return KindRateLimited
	case st >= 500:
		return KindUpstream
	case st >= 400:
		return KindRejected
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return KindTimeout
	}
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	var ci x509.CertificateInvalidError
	var tv *net.OpError
	if errors.As(err, &ua) || errors.As(err, &hn) || errors.As(err, &ci) {
		return KindTLS
	}
	var dns *net.DNSError
	if errors.As(err, &dns) || errors.As(err, &tv) {
		return KindConnection
	}
	return KindOther
}
