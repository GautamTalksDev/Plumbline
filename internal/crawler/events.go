package crawler

import (
	"errors"

	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

// EventsVersionV2 is recorded on every new observation row.
const EventsVersionV2 = "events-v2"

// Fixed diagnostic strings. Never interpolate server-supplied text into these.
const (
	DiagOK            = ""
	DiagAuthRequired  = "http_unauthorized"
	DiagEndpoint404   = "http_not_found"
	DiagDNS           = "dns_lookup_failed"
	DiagTLS           = "tls_handshake_failed"
	DiagHTTPError     = "http_non_2xx"
	DiagProtocol      = "mcp_handshake_failed"
	DiagMalformed     = "invalid_jsonrpc"
	DiagTimeout       = "deadline_exceeded"
	DiagSandbox       = "containment_failed"
	DiagRefused       = "connection_refused"
	DiagNoTarget      = "no_dialable_target"
	DiagTooLarge      = "response_too_large"
	DiagOptOut        = "opt_out"
)

// Classified is a crawl event with a fixed diagnostic and optional HTTP status.
type Classified struct {
	Event      string
	Diagnostic string
	HTTPStatus *int
}

// Classify converts mcpclient errors into mutually exclusive event types.
// Diagnostic is always from the fixed set above.
func Classify(err error) Classified {
	if err == nil {
		return Classified{Event: EventOK, Diagnostic: DiagOK}
	}

	var se *mcpclient.StatusError
	if errors.As(err, &se) {
		st := se.Status
		switch se.Kind {
		case mcpclient.StatusAuth:
			return Classified{Event: EventAuthRequired, Diagnostic: DiagAuthRequired, HTTPStatus: &st}
		case mcpclient.StatusNotFound:
			return Classified{Event: EventEndpoint404, Diagnostic: DiagEndpoint404, HTTPStatus: &st}
		default:
			return Classified{Event: EventHTTPError, Diagnostic: DiagHTTPError, HTTPStatus: &st}
		}
	}

	switch {
	case errors.Is(err, mcpclient.ErrDNS):
		return Classified{Event: EventDNS, Diagnostic: DiagDNS}
	case errors.Is(err, mcpclient.ErrTLS):
		return Classified{Event: EventTLS, Diagnostic: DiagTLS}
	case errors.Is(err, mcpclient.ErrTimeout):
		return Classified{Event: EventTimeout, Diagnostic: DiagTimeout}
	case errors.Is(err, mcpclient.ErrMalformed):
		return Classified{Event: EventMalformed, Diagnostic: DiagMalformed}
	case errors.Is(err, mcpclient.ErrTooLarge):
		return Classified{Event: EventTooLarge, Diagnostic: DiagTooLarge}
	case errors.Is(err, mcpclient.ErrProtocol):
		return Classified{Event: EventProtocol, Diagnostic: DiagProtocol}
	case errors.Is(err, mcpclient.ErrRefused):
		return Classified{Event: EventRefused, Diagnostic: DiagRefused}
	case errors.Is(err, mcpclient.ErrTransport):
		// Legacy transport sentinel (stdio dial, etc.) → refused.
		return Classified{Event: EventRefused, Diagnostic: DiagRefused}
	default:
		return Classified{Event: EventRefused, Diagnostic: DiagRefused}
	}
}

// MapEvent is retained for callers that only need event + diagnostic strings.
// Prefer Classify when HTTP status is needed.
func MapEvent(err error) (event, diagnostic string) {
	c := Classify(err)
	return c.Event, c.Diagnostic
}

func observationFromClassified(serverID string, c Classified) Observation {
	return Observation{
		ServerID:      serverID,
		EventType:     c.Event,
		Detail:        c.Diagnostic, // Detail holds the fixed diagnostic string
		Diagnostic:    c.Diagnostic,
		HTTPStatus:    c.HTTPStatus,
		EventsVersion: EventsVersionV2,
	}
}

func isFixedDiagnostic(d string) bool {
	switch d {
	case DiagOK, DiagAuthRequired, DiagEndpoint404, DiagDNS, DiagTLS, DiagHTTPError,
		DiagProtocol, DiagMalformed, DiagTimeout, DiagSandbox, DiagRefused,
		DiagNoTarget, DiagTooLarge, DiagOptOut:
		return true
	default:
		return false
	}
}

func fixedDiagnosticForEvent(ev string) string {
	switch ev {
	case EventOK:
		return DiagOK
	case EventAuthRequired:
		return DiagAuthRequired
	case EventEndpoint404:
		return DiagEndpoint404
	case EventDNS:
		return DiagDNS
	case EventTLS:
		return DiagTLS
	case EventHTTPError:
		return DiagHTTPError
	case EventProtocol:
		return DiagProtocol
	case EventMalformed:
		return DiagMalformed
	case EventTimeout:
		return DiagTimeout
	case EventSandbox:
		return DiagSandbox
	case EventRefused:
		return DiagRefused
	case EventTooLarge:
		return DiagTooLarge
	case EventOptOut:
		return DiagOptOut
	default:
		return DiagSandbox
	}
}
