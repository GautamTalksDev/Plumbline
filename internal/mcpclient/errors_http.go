package mcpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
)

// Typed failure classes for crawl event mapping. Error() never includes
// server-supplied response bodies.
var (
	ErrDNS     = errors.New("mcpclient: dns failure")
	ErrTLS     = errors.New("mcpclient: tls failure")
	ErrRefused = errors.New("mcpclient: connection refused")
)

// StatusKind classifies an HTTP status for the crawler.
type StatusKind int

const (
	StatusOther StatusKind = iota
	StatusAuth
	StatusNotFound
)

// StatusError is an HTTP response with a non-2xx status.
// Error() never includes the response body.
type StatusError struct {
	Status int
	Kind   StatusKind
}

func (e *StatusError) Error() string {
	if e == nil {
		return "mcpclient: http error"
	}
	return fmt.Sprintf("mcpclient: http %d", e.Status)
}

func mapHTTPDoError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w", ErrTimeout)
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return fmt.Errorf("%w", ErrDNS)
	}
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return fmt.Errorf("%w", ErrTLS)
	}
	if isConnRefused(err) {
		return fmt.Errorf("%w", ErrRefused)
	}
	ls := strings.ToLower(err.Error())
	switch {
	case strings.Contains(ls, "no such host") || strings.Contains(ls, "server misbehaving") ||
		strings.Contains(ls, "name resolution") || strings.Contains(ls, "temporary failure in name resolution"):
		return fmt.Errorf("%w", ErrDNS)
	case strings.Contains(ls, "certificate") || strings.Contains(ls, "tls:") ||
		strings.Contains(ls, "x509:"):
		return fmt.Errorf("%w", ErrTLS)
	case strings.Contains(ls, "connection refused"):
		return fmt.Errorf("%w", ErrRefused)
	default:
		return fmt.Errorf("%w", ErrRefused)
	}
}

func isConnRefused(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		err = op.Err
	}
	var sys *os.SyscallError
	if errors.As(err, &sys) {
		return errors.Is(sys.Err, syscall.ECONNREFUSED)
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

func statusError(code int, body []byte) error {
	kind := StatusOther
	switch {
	case code == 401 || code == 403:
		kind = StatusAuth
	case code == 404:
		kind = StatusNotFound
	case code == 400 && looksLikeMissingRoute(body):
		kind = StatusNotFound
	}
	return &StatusError{Status: code, Kind: kind}
}

// looksLikeMissingRoute inspects a response body only to choose a StatusKind.
// The body is never returned in StatusError.Error().
func looksLikeMissingRoute(body []byte) bool {
	ls := strings.ToLower(string(body))
	return strings.Contains(ls, "not found") ||
		strings.Contains(ls, "no such") ||
		strings.Contains(ls, "cannot post") ||
		strings.Contains(ls, "no server available") ||
		strings.Contains(ls, `"code":"404"`) ||
		strings.Contains(ls, `"status":404`)
}
