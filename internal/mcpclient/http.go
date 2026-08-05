package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type httpTransport struct {
	client    *http.Client
	url       string
	headers   map[string]string
	sessionID string
	protoVer  string
	mu        sync.Mutex
}

func dialHTTP(_ context.Context, spec ServerSpec) (transport, error) {
	if spec.URL == "" {
		return nil, fmt.Errorf("%w: http requires URL", ErrProtocol)
	}
	return &httpTransport{
		client:  &http.Client{Timeout: 0},
		url:     spec.URL,
		headers: spec.Headers,
	}, nil
}

func (t *httpTransport) roundTrip(ctx context.Context, req json.RawMessage) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if isNotification(req) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(req))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTransport, err)
		}
		t.applyHeaders(httpReq)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := t.client.Do(httpReq)
		if err != nil {
			return nil, mapHTTPDoError(err)
		}
		_, _ = io.Copy(io.Discard, newLimitedReader(resp.Body, MaxResponseBytes))
		_ = resp.Body.Close()
		return json.RawMessage(`{"jsonrpc":"2.0","id":null,"result":{}}`), nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(req))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	t.applyHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, mapHTTPDoError(err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID = sid
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := readAllLimited(resp.Body, 4096)
		return nil, statusError(resp.StatusCode, body)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return readSSEJSONRPC(ctx, resp.Body)
	}

	body, err := readAllLimited(resp.Body, MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		// Empty body on 2xx after POST is not "unreachable"; treat as protocol.
		return nil, fmt.Errorf("%w: empty HTTP body", ErrProtocol)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("%w: HTTP body", ErrMalformed)
	}
	out := make([]byte, len(body))
	copy(out, body)
	return out, nil
}

func (t *httpTransport) applyHeaders(req *http.Request) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.protoVer != "" {
		req.Header.Set("MCP-Protocol-Version", t.protoVer)
	}
}

func (t *httpTransport) close() error { return nil }

func (t *httpTransport) SetProtocolVersion(v string) {
	t.mu.Lock()
	t.protoVer = v
	t.mu.Unlock()
}

func readSSEJSONRPC(ctx context.Context, r io.Reader) (json.RawMessage, error) {
	lr := newLimitedReader(r, MaxResponseBytes)
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w", ErrTimeout)
		}
		n, err := lr.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if msg, ok := extractSSEDataJSON(buf); ok {
				if !json.Valid(msg) {
					return nil, fmt.Errorf("%w: SSE data", ErrMalformed)
				}
				return msg, nil
			}
		}
		if err == io.EOF {
			if msg, ok := extractSSEDataJSON(buf); ok {
				if !json.Valid(msg) {
					return nil, fmt.Errorf("%w: SSE data", ErrMalformed)
				}
				return msg, nil
			}
			// POST returned 2xx SSE that ended without a JSON-RPC frame:
			// transport reached the server; classify as protocol, not transport.
			return nil, fmt.Errorf("%w: empty SSE response", ErrProtocol)
		}
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: SSE read failed", ErrTransport)
		}
	}
}

func extractSSEDataJSON(buf []byte) (json.RawMessage, bool) {
	// Normalize CRLF so Cloudflare/Node streams parse like LF-only frames.
	s := string(buf)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	for {
		idx := strings.Index(s, "data:")
		if idx < 0 {
			return nil, false
		}
		rest := s[idx+len("data:"):]
		rest = strings.TrimPrefix(rest, " ")
		end := strings.Index(rest, "\n\n")
		if end < 0 {
			return nil, false
		}
		payload := strings.TrimSpace(strings.TrimSuffix(rest[:end], "\n"))
		if payload != "" && payload != "[DONE]" {
			b := []byte(payload)
			out := make([]byte, len(b))
			copy(out, b)
			return out, true
		}
		s = rest[end+2:]
	}
}
