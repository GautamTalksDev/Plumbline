package verify_test

import (
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

func seedBundle(t *testing.T, n int) (pub ed25519.PublicKey, bundleDir string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.sqlite")
	lg, err := log.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
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
		if _, err := lg.Append(log.Entry{
			ServerID:        "demo/server",
			ToolName:        "tool",
			Canonical:       c,
			Hash:            h,
			ProtocolVersion: "2024-11-05",
			CanonVersion:    log.CanonVersionV1,
		}); err != nil {
			t.Fatal(err)
		}
		if i == 2 || i == n-1 {
			if _, err := lg.SignCheckpoint(priv); err != nil {
				t.Fatal(err)
			}
		}
	}
	lg.Close()

	out := filepath.Join(dir, "bundle")
	if err := publish.Bundle(publish.Options{
		LogPath: logPath,
		OutDir:  out,
		Week:    1,
		PubKey:  pub,
	}); err != nil {
		t.Fatal(err)
	}
	return pub, out
}

func TestVerifyBundleChecksInclusionSample(t *testing.T) {
	pub, out := seedBundle(t, 12)
	res, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:     out,
		PublicKey:  pub,
		MaxEntries: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected OK: %+v", res)
	}
	if res.InclusionProofs != 12 {
		t.Fatalf("inclusion_proofs=%d want 12", res.InclusionProofs)
	}
	if res.CheckedEntries != 12 {
		t.Fatalf("checked=%d want 12", res.CheckedEntries)
	}
	if !res.InclusionOK {
		t.Fatal("inclusion should be true")
	}
	if res.InclusionOK && res.CheckedEntries == 0 {
		t.Fatal("inclusion=true with checked=0 is forbidden")
	}
}

func TestVerifyBundleNeverInclusionTrueWithZeroCheckedWhenProofsExist(t *testing.T) {
	pub, out := seedBundle(t, 8)

	// Force the failure class: MaxEntries <= 0 is normalized to 500, so
	// instead remove all proof files after recording they existed, then
	// put one proof back that cannot be verified path — actually the
	// invariant under test is: if proofs exist, InclusionOK implies Checked>0.
	res, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:     out,
		PublicKey:  pub,
		MaxEntries: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.InclusionProofs == 0 {
		t.Fatal("fixture must contain proofs")
	}
	if res.InclusionOK && res.CheckedEntries == 0 {
		t.Fatalf("bundle with proofs must never report inclusion=true with checked=0; got %+v", res)
	}
	if !res.OK || !res.InclusionOK || res.CheckedEntries == 0 {
		t.Fatalf("expected successful sample check, got %+v", res)
	}
	if res.CheckedEntries != 3 {
		t.Fatalf("checked=%d want 3 (max-entries)", res.CheckedEntries)
	}
	if res.InclusionProofs != 8 {
		t.Fatalf("proofs=%d want 8", res.InclusionProofs)
	}
}

func TestVerifyBundleSkippedWhenNoProofs(t *testing.T) {
	pub, out := seedBundle(t, 4)
	incDir := filepath.Join(out, "proofs", "inclusion")
	entries, err := os.ReadDir(incDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(incDir, e.Name()))
	}

	res, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:    out,
		PublicKey: pub,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.InclusionOK {
		t.Fatal("inclusion must not be true when 0 proofs found")
	}
	if !res.InclusionSkipped {
		t.Fatal("expected InclusionSkipped")
	}
	if res.CheckedEntries != 0 {
		t.Fatalf("checked=%d", res.CheckedEntries)
	}
}

func TestVerifyBundleCorruptedInclusionFailsNamingIndex(t *testing.T) {
	pub, out := seedBundle(t, 6)
	target := filepath.Join(out, "proofs", "inclusion", "2.json")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var proof map[string]any
	if err := json.Unmarshal(raw, &proof); err != nil {
		t.Fatal(err)
	}
	lh, _ := proof["leaf_hash"].(string)
	b := []byte(lh)
	if b[0] == '0' {
		b[0] = '1'
	} else {
		b[0] = '0'
	}
	proof["leaf_hash"] = string(b)
	outb, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, append(outb, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	// Check all proofs so idx 2 is included.
	res, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:     out,
		PublicKey:  pub,
		MaxEntries: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.InclusionOK {
		t.Fatalf("expected inclusion failure, got %+v", res)
	}
	joined := strings.Join(res.Errors, "; ")
	if !strings.Contains(joined, "idx 2") {
		t.Fatalf("error should name failing index 2, got %q", joined)
	}

	bin := filepath.Join(t.TempDir(), "plumb-test")
	build := exec.Command("go", "build", "-o", bin, "./cmd/plumb")
	build.Dir = repoRoot(t)
	if outb, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, outb)
	}
	cmd := exec.Command(bin, "verify-log", "--bundle", out, "--pubkey-hex", hex.EncodeToString(pub), "--max-entries", "500")
	err = cmd.Run()
	if err == nil {
		t.Fatal("expected exit 1")
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("exit=%v", err)
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
