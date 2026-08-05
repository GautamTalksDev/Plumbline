package verify_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/canonical"
	"github.com/GautamTalksDev/Plumbline/internal/httpserver"
	"github.com/GautamTalksDev/Plumbline/internal/log"
	"github.com/GautamTalksDev/Plumbline/internal/verify"
	_ "modernc.org/sqlite"
)

func seedLog(t *testing.T, path string, priv ed25519.PrivateKey, n int) *log.Log {
	t.Helper()
	lg, err := log.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		desc := fmt.Sprintf("hello-%d", i)
		if i == 3 {
			desc = `<script>alert(1)</script>`
		}
		raw := []byte(fmt.Sprintf(`{"name":"t","description":%q}`, desc))
		c, err := canonical.Canonicalize(raw)
		if err != nil {
			t.Fatal(err)
		}
		h, err := canonical.Hash(raw)
		if err != nil {
			t.Fatal(err)
		}
		_, err = lg.Append(log.Entry{
			ServerID:        "demo/server",
			ToolName:        "tool",
			Canonical:       c,
			Hash:            h,
			ProtocolVersion: "2024-11-05",
			CanonVersion:    log.CanonVersionV1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 || i == n-1 {
			if _, err := lg.SignCheckpoint(priv); err != nil {
				t.Fatal(err)
			}
		}
	}
	return lg
}

func TestVerifyOK(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "log.sqlite")
	lg := seedLog(t, path, priv, 6)
	defer lg.Close()

	srv := httpserver.New(httpserver.Config{Log: lg, PubKey: pub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := verify.Verify(context.Background(), verify.Options{
		URL:        ts.URL,
		PublicKey:  pub,
		MaxEntries: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "log.sqlite")
	lg := seedLog(t, path, priv, 6)
	lg.Close()

	// Hand-edit sqlite: disable triggers and corrupt a leaf_hash.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(`DROP TRIGGER IF EXISTS entries_no_update`)
	_, err = db.Exec(`UPDATE entries SET leaf_hash = ? WHERE idx = 1`, hex.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	lg2, err := log.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg2.Close()

	srv := httpserver.New(httpserver.Config{Log: lg2, PubKey: pub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := verify.Verify(context.Background(), verify.Options{
		URL:        ts.URL,
		PublicKey:  pub,
		MaxEntries: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("expected verification failure after hand-editing sqlite leaf_hash")
	}
	if len(res.Errors) == 0 {
		t.Fatal("expected error details")
	}
	t.Logf("detected: %v", res.Errors)
}

func TestVerifyDetectsBadCheckpointSig(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "log.sqlite")
	lg := seedLog(t, path, priv, 4)
	lg.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE checkpoints SET signature = ?`, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	lg2, err := log.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg2.Close()

	srv := httpserver.New(httpserver.Config{Log: lg2, PubKey: pub})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := verify.Verify(context.Background(), verify.Options{
		URL:       ts.URL,
		PublicKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.CheckpointOK {
		t.Fatalf("expected checkpoint failure, got %+v", res)
	}
}
