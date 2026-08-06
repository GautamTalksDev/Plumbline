package crawler

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

func initResult(id any) []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "fake", "version": "0"},
		},
	})
	return b
}

func toolsListResult(id any) []byte {
	tool := json.RawMessage(`{"name":"echo","description":"d","inputSchema":{"type":"object"}}`)
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  map[string]any{"tools": []json.RawMessage{tool}},
	})
	return b
}

func rpcID(body []byte) any {
	var m struct {
		ID any `json:"id"`
	}
	_ = json.Unmarshal(body, &m)
	return m.ID
}

func rpcMethod(body []byte) string {
	var m struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Method
}

func observeURL(t *testing.T, rawURL string) Observation {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return observeHTTPAllowlisted(context.Background(), Target{
		ServerID:  "test/server",
		URL:       rawURL,
		AllowHost: u.Host,
		Timeout:   5 * time.Second,
	})
}

func assertFixedDiag(t *testing.T, obs Observation) {
	t.Helper()
	diag := obs.Diagnostic
	if diag == "" {
		diag = obs.Detail
	}
	if !isFixedDiagnostic(diag) {
		t.Fatalf("diagnostic %q not in fixed set", diag)
	}
	if obs.EventsVersion != EventsVersionV2 {
		t.Fatalf("events_version=%q", obs.EventsVersion)
	}
}

func TestEventsV2_AuthRequired(t *testing.T) {
	for _, code := range []int{401, 403} {
		code := code
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"SECRET_TOKEN_xyz never_in_diag"}`))
		}))
		obs := observeURL(t, ts.URL)
		ts.Close()
		if obs.EventType != EventAuthRequired {
			t.Fatalf("status %d: event=%s", code, obs.EventType)
		}
		if obs.HTTPStatus == nil || *obs.HTTPStatus != code {
			t.Fatalf("status %d: http_status=%v", code, obs.HTTPStatus)
		}
		if obs.Diagnostic != DiagAuthRequired {
			t.Fatalf("diag=%q", obs.Diagnostic)
		}
		if strings.Contains(obs.Diagnostic, "SECRET") || strings.Contains(obs.Detail, "SECRET") {
			t.Fatal("server body leaked into diagnostic")
		}
		assertFixedDiag(t, obs)
	}
}

func TestEventsV2_Endpoint404(t *testing.T) {
	t.Run("404", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`not found: /secret-path-abc`))
		}))
		defer ts.Close()
		obs := observeURL(t, ts.URL)
		if obs.EventType != EventEndpoint404 {
			t.Fatalf("event=%s", obs.EventType)
		}
		if obs.HTTPStatus == nil || *obs.HTTPStatus != 404 {
			t.Fatalf("http_status=%v", obs.HTTPStatus)
		}
		if strings.Contains(obs.Diagnostic, "secret-path") {
			t.Fatal("server body leaked")
		}
		assertFixedDiag(t, obs)
	})
	t.Run("400_missing_route", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"message":"Cannot POST /mcp — no such route"}`))
		}))
		defer ts.Close()
		obs := observeURL(t, ts.URL)
		if obs.EventType != EventEndpoint404 {
			t.Fatalf("event=%s", obs.EventType)
		}
		assertFixedDiag(t, obs)
	})
}

func TestEventsV2_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`upstream exploded: internal id 99`))
	}))
	defer ts.Close()
	obs := observeURL(t, ts.URL)
	if obs.EventType != EventHTTPError {
		t.Fatalf("event=%s", obs.EventType)
	}
	if obs.HTTPStatus == nil || *obs.HTTPStatus != 502 {
		t.Fatalf("http_status=%v", obs.HTTPStatus)
	}
	if obs.Diagnostic != DiagHTTPError {
		t.Fatalf("diag=%q", obs.Diagnostic)
	}
	if strings.Contains(obs.Diagnostic, "exploded") {
		t.Fatal("server body leaked")
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_DNSFailure(t *testing.T) {
	obs := observeHTTPAllowlisted(context.Background(), Target{
		ServerID:  "test/dns",
		URL:       "http://no-such-host.invalid./mcp",
		AllowHost: "no-such-host.invalid.",
		Timeout:   10 * time.Second,
	})
	if obs.EventType != EventDNS {
		t.Fatalf("event=%s diag=%s", obs.EventType, obs.Diagnostic)
	}
	if obs.Diagnostic != DiagDNS {
		t.Fatalf("diag=%q", obs.Diagnostic)
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_TLSFailure(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(initResult(1))
	}))
	defer ts.Close()
	// Default client rejects the httptest certificate.
	obs := observeURL(t, ts.URL)
	if obs.EventType != EventTLS {
		t.Fatalf("event=%s diag=%s", obs.EventType, obs.Diagnostic)
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_Refused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	obs := observeURL(t, "http://"+addr+"/mcp")
	if obs.EventType != EventRefused {
		t.Fatalf("event=%s diag=%s", obs.EventType, obs.Diagnostic)
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_Timeout(t *testing.T) {
	// Server is reachable; the probe context is already expired so dial/POST aborts.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(initResult(rpcID(body)))
	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	obs := observeHTTPAllowlisted(ctx, Target{
		ServerID:  "test/timeout",
		URL:       ts.URL,
		AllowHost: u.Host,
	})
	if obs.EventType != EventTimeout {
		t.Fatalf("event=%s diag=%s", obs.EventType, obs.Diagnostic)
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_ProtocolError_EmptySSE(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Empty SSE stream after successful transport.
	}))
	defer ts.Close()
	obs := observeURL(t, ts.URL)
	if obs.EventType != EventProtocol {
		t.Fatalf("event=%s", obs.EventType)
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_Malformed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not-json{{{`))
	}))
	defer ts.Close()
	obs := observeURL(t, ts.URL)
	if obs.EventType != EventMalformed {
		t.Fatalf("event=%s", obs.EventType)
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_ContainmentFailed(t *testing.T) {
	obs := observeHTTPAllowlisted(context.Background(), Target{
		ServerID: "test/sandbox",
		URL:      "http://127.0.0.1:9/mcp",
		// AllowHost intentionally empty → containment config failed.
	})
	if obs.EventType != EventContainmentFailed {
		t.Fatalf("event=%s", obs.EventType)
	}
	if obs.Diagnostic != DiagContainmentFailed {
		t.Fatalf("diag=%q", obs.Diagnostic)
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_GetEmptyPostValid_OK(t *testing.T) {
	// Servers may only respond on POST; empty GET SSE must not be treated as failure.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			return
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			method := rpcMethod(body)
			id := rpcID(body)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "s1")
			switch method {
			case "initialize":
				_, _ = w.Write(initResult(id))
			case "tools/list":
				_, _ = w.Write(toolsListResult(id))
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			default:
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"result":{}}`))
			}
			return
		default:
			w.WriteHeader(405)
		}
	}))
	defer ts.Close()

	obs := observeURL(t, ts.URL)
	if obs.EventType != EventOK {
		t.Fatalf("event=%s diag=%s (GET-empty/POST-valid must be ok)", obs.EventType, obs.Diagnostic)
	}
	if len(obs.Tools) != 1 {
		t.Fatalf("tools=%d", len(obs.Tools))
	}
	assertFixedDiag(t, obs)
}

func TestEventsV2_SSE_CRLF_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		method := rpcMethod(body)
		id := rpcID(body)
		var payload []byte
		switch method {
		case "initialize":
			payload = initResult(id)
		case "tools/list":
			payload = toolsListResult(id)
		default:
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "s1")
		// CRLF framing as returned by many production edge proxies.
		_, _ = io.WriteString(w, "event: message\r\ndata: "+string(payload)+"\r\n\r\n")
	}))
	defer ts.Close()
	obs := observeURL(t, ts.URL)
	if obs.EventType != EventOK {
		t.Fatalf("event=%s diag=%s", obs.EventType, obs.Diagnostic)
	}
}

func TestClassify_StatusErrorNeverIncludesBody(t *testing.T) {
	err := &mcpclient.StatusError{Status: 401, Kind: mcpclient.StatusAuth}
	if strings.Contains(err.Error(), "token") {
		t.Fatal(err)
	}
	c := Classify(err)
	if c.Event != EventAuthRequired || c.Diagnostic != DiagAuthRequired {
		t.Fatalf("%+v", c)
	}
}

func TestEventCanonical_EventsV2(t *testing.T) {
	st := 401
	obs := Observation{
		ServerID:      "a/b",
		EventType:     EventAuthRequired,
		Diagnostic:    DiagAuthRequired,
		HTTPStatus:    &st,
		EventsVersion: EventsVersionV2,
	}
	c, _, err := eventCanonical(obs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(c)
	if !strings.Contains(s, "events-v2") {
		t.Fatalf("missing events-v2 in canonical: %s", s)
	}
	if !strings.Contains(s, "auth_required") {
		t.Fatalf("missing event: %s", s)
	}
	if !strings.Contains(s, "http_unauthorized") {
		t.Fatalf("missing diagnostic: %s", s)
	}
}
