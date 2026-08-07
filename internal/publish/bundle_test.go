package publish_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/canonical"
	"github.com/GautamTalksDev/Plumbline/internal/log"
	"github.com/GautamTalksDev/Plumbline/internal/publish"
	"github.com/GautamTalksDev/Plumbline/internal/verify"
)

func seed(t *testing.T, path string, priv ed25519.PrivateKey, n int) {
	t.Helper()
	lg, err := log.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	for i := 0; i < n; i++ {
		raw := []byte(fmt.Sprintf(`{"name":"t","description":"hello-%d"}`, i))
		c, err := canonical.Canonicalize(raw)
		if err != nil {
			t.Fatal(err)
		}
		h, err := canonical.Hash(raw)
		if err != nil {
			t.Fatal(err)
		}
		sid := "demo/server"
		if i%2 == 0 {
			sid = `evil/<script>alert(1)</script>`
		}
		_, err = lg.Append(log.Entry{
			ServerID:        sid,
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
}

func TestBundleAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.sqlite")
	seed(t, logPath, priv, 6)

	out := filepath.Join(dir, "bundle")
	if err := publish.Bundle(publish.Options{
		LogPath: logPath,
		OutDir:  out,
		Week:    1,
		PubKey:  pub,
	}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"checkpoint.json",
		"checkpoints.json",
		"pubkey.txt",
		"meta.json",
		"servers/index.json",
	} {
		if _, err := os.Stat(filepath.Join(out, rel)); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}

	var meta publish.Meta
	b, err := os.ReadFile(filepath.Join(out, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Week != 1 || meta.WeekTotal != 6 {
		t.Fatalf("meta week: %+v", meta)
	}
	if meta.TreeSize != 6 {
		t.Fatalf("tree_size=%d", meta.TreeSize)
	}

	res, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:    out,
		PublicKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("verify failed: %+v", res)
	}
	if res.InclusionProofs == 0 {
		t.Fatal("expected inclusion proofs in bundle")
	}
	if res.InclusionOK && res.CheckedEntries == 0 {
		t.Fatal("inclusion=true with checked=0 is forbidden")
	}
	if res.CheckedEntries == 0 {
		t.Fatal("expected at least one inclusion proof checked")
	}

	idxB, err := os.ReadFile(filepath.Join(out, "servers", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx publish.ServerIndex
	if err := json.Unmarshal(idxB, &idx); err != nil {
		t.Fatal(err)
	}
	for _, s := range idx.Servers {
		if strings.ContainsAny(s.File, "<>/") {
			t.Fatalf("unsafe server filename %q", s.File)
		}
		raw, err := os.ReadFile(filepath.Join(out, "servers", s.File))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(`"description"`)) {
			t.Fatal("server history must not republish descriptions")
		}
	}
}

func TestVerifyBundleDetectsTamperedRoot(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.sqlite")
	seed(t, logPath, priv, 6)
	out := filepath.Join(dir, "bundle")
	if err := publish.Bundle(publish.Options{
		LogPath: logPath,
		OutDir:  out,
		Week:    1,
		PubKey:  pub,
	}); err != nil {
		t.Fatal(err)
	}

	cpPath := filepath.Join(out, "checkpoints.json")
	raw, err := os.ReadFile(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	var file publish.CheckpointsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Checkpoints) < 1 {
		t.Fatal("need checkpoints")
	}
	rh := []byte(file.Checkpoints[len(file.Checkpoints)-1].RootHash)
	if rh[0] == '0' {
		rh[0] = '1'
	} else {
		rh[0] = '0'
	}
	file.Checkpoints[len(file.Checkpoints)-1].RootHash = string(rh)
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cpPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:    out,
		PublicKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("expected verification failure after flipping root hash byte")
	}
	if len(res.Errors) == 0 {
		t.Fatal("expected error naming the failed checkpoint")
	}
	joined := strings.Join(res.Errors, "; ")
	if !strings.Contains(joined, "tree_size") && !strings.Contains(joined, "checkpoint") {
		t.Fatalf("error should name checkpoint, got %q", joined)
	}

	bin := filepath.Join(dir, "plumb-test")
	build := exec.Command("go", "build", "-o", bin, "./cmd/plumb")
	build.Dir = repoRoot(t)
	if outb, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plumb: %v\n%s", err, outb)
	}
	cmd := exec.Command(bin, "verify-log", "--bundle", out, "--pubkey-hex", hex.EncodeToString(pub))
	err = cmd.Run()
	if err == nil {
		t.Fatal("plumb verify-log --bundle should exit non-zero on tampered checkpoints.json")
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() != 1 {
			t.Fatalf("exit code=%d want 1", ee.ExitCode())
		}
	} else {
		t.Fatalf("unexpected error: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
