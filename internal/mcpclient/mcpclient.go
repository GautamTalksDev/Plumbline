// Package mcpclient connects to MCP servers and retrieves tool surfaces safely.
//
// Tool definitions are retained as json.RawMessage. Never unmarshal a tool into
// a Go struct and re-marshal before hashing: unknown fields are where poisoning hides.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	// ConnectDeadline is the total deadline for Connect (dial + initialize).
	ConnectDeadline = 30 * time.Second
	// RequestTimeout is the per-request deadline (e.g. tools/list).
	RequestTimeout = 10 * time.Second
	// MaxResponseBytes caps any single response. Exceeding is an error, never truncate.
	MaxResponseBytes = 8 << 20

	clientName    = "plumbline"
	clientVersion = "0.0.0"
	// Preferred protocol version offered during initialize.
	preferredProtocolVersion = "2024-11-05"
)

// Sentinel errors recorded as log events. Use errors.Is.
var (
	ErrTimeout    = errors.New("mcpclient: timeout")
	ErrTooLarge   = errors.New("mcpclient: response too large")
	ErrMalformed  = errors.New("mcpclient: malformed JSON")
	ErrTransport  = errors.New("mcpclient: transport error")
	ErrProtocol   = errors.New("mcpclient: protocol error")
)

// Transport names for ServerSpec.Transport.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// ServerSpec describes how to reach an MCP server.
type ServerSpec struct {
	Transport string // "stdio" or "http"

	// Stdio
	Command string
	Args    []string
	Env     []string // optional extra env KEY=VAL
	Dir     string

	// Streamable HTTP
	URL     string
	Headers map[string]string
}

// ToolList is the tools/list result with each tool kept as raw JSON.
type ToolList struct {
	ProtocolVersion string
	Tools           []json.RawMessage // raw tool objects; never re-encoded from structs
}

// Session is a live MCP connection after successful initialize.
type Session struct {
	ProtocolVersion string

	transport transport
	nextID    int64
}

type transport interface {
	roundTrip(ctx context.Context, req json.RawMessage) (json.RawMessage, error)
	close() error
}

// Connect dials the server, performs initialize, and returns a Session.
// Dial + initialize are bounded by ConnectDeadline (30s) unless ctx
// cancels sooner. The subprocess lifetime follows ctx (and Close), not
// the connect deadline. Malformed JSON is not retried.
func Connect(ctx context.Context, spec ServerSpec) (*Session, error) {
	connectCtx, cancel := context.WithTimeout(ctx, ConnectDeadline)
	defer cancel()

	var tr transport
	var err error
	switch spec.Transport {
	case TransportStdio, "":
		if spec.Transport == "" && spec.URL != "" && spec.Command == "" {
			tr, err = dialHTTP(connectCtx, spec)
		} else {
			if spec.Command == "" {
				return nil, fmt.Errorf("%w: stdio requires Command", ErrProtocol)
			}
			// Process lifetime is bound to ctx, not connectCtx.
			tr, err = dialStdio(ctx, spec)
		}
	case TransportHTTP:
		tr, err = dialHTTP(connectCtx, spec)
	default:
		return nil, fmt.Errorf("%w: unknown transport %q", ErrProtocol, spec.Transport)
	}
	if err != nil {
		return nil, err
	}

	s := &Session{transport: tr, nextID: 1}
	if err := s.initialize(connectCtx); err != nil {
		_ = tr.close()
		return nil, err
	}
	if ht, ok := tr.(*httpTransport); ok {
		ht.SetProtocolVersion(s.ProtocolVersion)
	}
	return s, nil
}

// ListTools calls tools/list and returns raw tool JSON objects.
func (s *Session) ListTools(ctx context.Context) (ToolList, error) {
	if s == nil || s.transport == nil {
		return ToolList{}, fmt.Errorf("%w: session closed", ErrTransport)
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()

	raw, err := s.call(ctx, "tools/list", json.RawMessage(`{}`))
	if err != nil {
		return ToolList{}, err
	}

	tools, err := extractToolsRaw(raw)
	if err != nil {
		return ToolList{}, err
	}
	return ToolList{
		ProtocolVersion: s.ProtocolVersion,
		Tools:           tools,
	}, nil
}

// Close terminates the session and underlying transport.
func (s *Session) Close() error {
	if s == nil || s.transport == nil {
		return nil
	}
	err := s.transport.close()
	s.transport = nil
	return err
}

func (s *Session) initialize(ctx context.Context) error {
	params, err := json.Marshal(map[string]any{
		"protocolVersion": preferredProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    clientName,
			"version": clientVersion,
		},
	})
	if err != nil {
		return fmt.Errorf("%w: marshal initialize: %v", ErrProtocol, err)
	}

	result, err := s.call(ctx, "initialize", params)
	if err != nil {
		return err
	}

	ver, err := extractProtocolVersion(result)
	if err != nil {
		return err
	}
	s.ProtocolVersion = ver

	// notifications/initialized — no response expected; ignore transport errors
	// after a successful initialize only if send fails hard.
	n, err := s.encodeNotification("notifications/initialized", json.RawMessage(`{}`))
	if err != nil {
		return err
	}
	if _, err := s.transport.roundTrip(ctx, n); err != nil {
		// Some servers ACK with empty body / EOF on notification; treat
		// timeout/malformed/too-large as real failures.
		if errors.Is(err, ErrTimeout) || errors.Is(err, ErrMalformed) || errors.Is(err, ErrTooLarge) {
			return err
		}
		// Notification may not yield a JSON-RPC response on stdio; dialStdio
		// handles that. HTTP may return 202/empty — also fine.
	}
	return nil
}

func (s *Session) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := s.nextID
	s.nextID++
	req, err := encodeRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	resp, err := s.transport.roundTrip(ctx, req)
	if err != nil {
		return nil, mapContextError(err)
	}
	return decodeResponseResult(resp, id)
}

func (s *Session) encodeNotification(method string, params json.RawMessage) (json.RawMessage, error) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return b, nil
}

func mapContextError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTimeout) || errors.Is(err, ErrTooLarge) ||
		errors.Is(err, ErrMalformed) || errors.Is(err, ErrTransport) ||
		errors.Is(err, ErrProtocol) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	return err
}
