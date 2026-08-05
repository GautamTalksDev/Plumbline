package httpserver_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/canonical"
	"github.com/GautamTalksDev/Plumbline/internal/httpserver"
	"github.com/GautamTalksDev/Plumbline/internal/log"
)

func TestAPIReadOnlyAndCheckpoint(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := log.Open(filepath.Join(t.TempDir(), "l.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	raw := []byte(`{"name":"x","description":"<script>alert(1)</script>"}`)
	c, _ := canonical.Canonicalize(raw)
	h, _ := canonical.Hash(raw)
	_, _ = lg.Append(log.Entry{
		ServerID: "v/s", ToolName: "x", Canonical: c, Hash: h,
		ProtocolVersion: "1", CanonVersion: log.CanonVersionV1,
	})
	_, err = lg.SignCheckpoint(priv)
	if err != nil {
		t.Fatal(err)
	}

	srv := httpserver.New(httpserver.Config{Log: lg, PubKey: pub, Site: nil})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// POST rejected
	resp, err := http.Post(ts.URL+"/checkpoint", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	var cp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cp); err != nil {
		t.Fatal(err)
	}
	if cp["tree_size"].(float64) != 1 {
		t.Fatalf("%v", cp)
	}

	resp, err = http.Get(ts.URL + "/pubkey")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "ed25519") {
		t.Fatalf("%s", body)
	}

	resp, err = http.Get(ts.URL + "/server/history?id=v/s")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "c2NyaXB0") { // base64 fragment of <script
		t.Fatalf("expected script tag in base64 canonical, got %s", body)
	}
}
