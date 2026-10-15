package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"
)

// Error classes say why a check failed. The set is Uptimy Cloud's (its
// healthchecker's errorclass package), so uploaded results read the same
// as Cloud's own.
const (
	classTimeout   = "timeout"   // no answer within the check's timeout
	classDNS       = "dns"       // the name didn't resolve
	classTLS       = "tls"       // handshake or certificate failure (incl. expiry)
	classConnect   = "connect"   // refused, reset or unreachable
	classHTTP5xx   = "http_5xx"  // the target answered with a server error
	classHTTP4xx   = "http_4xx"  // the target answered with a client error
	classAssertion = "assertion" // it answered, but not as the check expects
	classOther     = "other"
)

// classForStatus is the class of an unexpected HTTP status code.
func classForStatus(code int) string {
	switch {
	case code >= 500 && code <= 599:
		return classHTTP5xx
	case code >= 400 && code <= 499:
		return classHTTP4xx
	default:
		return classAssertion
	}
}

// classify derives the class of a transport error from the network error
// it wraps. nil is "".
func classify(err error) string {
	if err == nil {
		return ""
	}
	// DNS before timeout: a lookup that timed out is still a DNS failure.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return classDNS
	}
	if isTLS(err) {
		return classTLS
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return classTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) {
		return classConnect
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return classConnect
	}
	return classOther
}

func isTLS(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	var recordHeader tls.RecordHeaderError
	var alert tls.AlertError
	return errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) ||
		errors.As(err, &verification) || errors.As(err, &recordHeader) || errors.As(err, &alert)
}

// failure is the outcome of a transport error, classified.
func failure(err error) Outcome {
	return Outcome{Message: trimErr(err), ErrorClass: classify(err)}
}
