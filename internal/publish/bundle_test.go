package publish_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/canonical"
	"github.com/GautamTalksDev/Plumbline/internal/log"
	"github.com/GautamTalksDev/Plumbline/internal/publish"
	"github.com/GautamTalksDev/Plumbline/internal/verify"
)

func seed(t *testing.T, path string, priv ed25519.PrivateKey, n int) {
	t.Helper()
	seedServers(t, path, priv, n, 2)
}

func seedServers(t *testing.T, path string, priv ed25519.PrivateKey, n, nServers int) {
	t.Helper()
	lg, err := log.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if nServers < 1 {
		nServers = 1
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
		sid := fmt.Sprintf("demo/server-%d", i%nServers)
		if i%2 == 0 {
			sid = fmt.Sprintf(`evil/<script>%d</script>`, i%nServers)
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
		"proofs/inclusion/index.json",
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

	var incIdx publish.InclusionIndex
	ib, err := os.ReadFile(filepath.Join(out, "proofs", "inclusion", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ib, &incIdx); err != nil {
		t.Fatal(err)
	}
	if incIdx.TotalEntries != 6 || incIdx.Count != 6 {
		t.Fatalf("inclusion index: %+v", incIdx)
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
		if !strings.HasSuffix(s.File, ".json") || len(s.File) != len("ab.json") {
			t.Fatalf("expected shard filename, got %q", s.File)
		}
		raw, err := os.ReadFile(filepath.Join(out, "servers", s.File))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(`"description"`)) {
			t.Fatal("server history must not republish descriptions")
		}
		var shard publish.ServerShard
		if err := json.Unmarshal(raw, &shard); err != nil {
			t.Fatal(err)
		}
		if _, ok := shard.Servers[s.ID]; !ok {
			t.Fatalf("server %q missing from shard %s", s.ID, s.File)
		}
	}
}

func TestBundleFileCountUnderCap(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.sqlite")
	// Large enough that a naive one-proof-per-entry publish would blow the cap.
	const n = 6000
	seedServers(t, logPath, priv, n, 800)

	out := filepath.Join(dir, "bundle")
	if err := publish.Bundle(publish.Options{
		LogPath: logPath,
		OutDir:  out,
		Week:    1,
		PubKey:  pub,
	}); err != nil {
		t.Fatal(err)
	}

	count, err := publish.CountBundleFiles(out)
	if err != nil {
		t.Fatal(err)
	}
	if count >= 5000 {
		t.Fatalf("bundle has %d files; must stay under 5000 for Cloudflare Pages", count)
	}

	var incIdx publish.InclusionIndex
	ib, err := os.ReadFile(filepath.Join(out, "proofs", "inclusion", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ib, &incIdx); err != nil {
		t.Fatal(err)
	}
	if incIdx.TotalEntries != n {
		t.Fatalf("total_entries=%d want %d", incIdx.TotalEntries, n)
	}
	if incIdx.Count >= n {
		t.Fatalf("sampled all %d entries — sampling rule not applied", incIdx.Count)
	}
	if incIdx.Count > publish.DefaultMaxProofs+publish.TailProofs+publish.DefaultMaxProofs {
		t.Fatalf("sample count %d unexpectedly large", incIdx.Count)
	}

	// Local + HTTP verify must agree and succeed.
	local, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:     out,
		PublicKey:  pub,
		MaxEntries: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !local.OK {
		t.Fatalf("local verify failed: %+v", local)
	}

	ts := httptest.NewServer(http.FileServer(http.Dir(out)))
	defer ts.Close()
	remote, err := verify.VerifyBundle(context.Background(), verify.BundleOptions{
		Bundle:     ts.URL,
		PublicKey:  pub,
		MaxEntries: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !remote.OK {
		t.Fatalf("HTTP verify failed: %+v", remote)
	}
	if local.CheckedEntries != remote.CheckedEntries {
		t.Fatalf("checked local=%d remote=%d", local.CheckedEntries, remote.CheckedEntries)
	}
	if local.CheckedEntries == 0 {
		t.Fatal("proofs present but zero checked must not pass")
	}
}

func TestFullPublishRemovesOrphanedProofs(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.sqlite")
	seedServers(t, logPath, priv, 3000, 40)

	out := filepath.Join(dir, "bundle")
	opt := publish.Options{LogPath: logPath, OutDir: out, Week: 1, PubKey: pub, MaxProofs: 500}
	if err := publish.Bundle(opt); err != nil {
		t.Fatal(err)
	}
	firstCount, err := publish.CountBundleFiles(out)
	if err != nil {
		t.Fatal(err)
	}
	var firstIdx publish.InclusionIndex
	if b, err := os.ReadFile(filepath.Join(out, "proofs", "inclusion", "index.json")); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &firstIdx); err != nil {
		t.Fatal(err)
	}
	if firstIdx.Count == 0 {
		t.Fatal("expected proofs in first publish")
	}

	// Simulate stale leftovers from an older one-proof-per-entry layout.
	stale := filepath.Join(out, "proofs", "inclusion", "39999.json")
	if err := os.WriteFile(stale, []byte(`{"index":39999}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyServer := filepath.Join(out, "servers", strings.Repeat("ab", 32)+".json")
	if err := os.WriteFile(legacyServer, []byte(`{}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opt.MaxProofs = 100
	if err := publish.Bundle(opt); err != nil {
		t.Fatal(err)
	}
	secondCount, err := publish.CountBundleFiles(out)
	if err != nil {
		t.Fatal(err)
	}
	if secondCount >= firstCount {
		t.Fatalf("file count did not decrease after smaller --max-proofs: first=%d second=%d (proofs %d→ need shrink)", firstCount, secondCount, firstIdx.Count)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("orphaned inclusion proof 39999.json still present after full publish")
	}
	if _, err := os.Stat(legacyServer); !os.IsNotExist(err) {
		t.Fatal("legacy per-server file still present after full publish")
	}

	var secondIdx publish.InclusionIndex
	if b, err := os.ReadFile(filepath.Join(out, "proofs", "inclusion", "index.json")); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &secondIdx); err != nil {
		t.Fatal(err)
	}
	if secondIdx.Count >= firstIdx.Count {
		t.Fatalf("proof count did not shrink: first=%d second=%d", firstIdx.Count, secondIdx.Count)
	}
	if secondIdx.MaxProofs != 100 {
		t.Fatalf("max_proofs=%d", secondIdx.MaxProofs)
	}

	// Every on-disk proof file must appear in the index (no orphans).
	entries, err := os.ReadDir(filepath.Join(out, "proofs", "inclusion"))
	if err != nil {
		t.Fatal(err)
	}
	indexed := map[int64]bool{}
	for _, i := range secondIdx.Indices {
		indexed[i] = true
	}
	onDisk := 0
	for _, e := range entries {
		name := e.Name()
		if name == "index.json" {
			continue
		}
		stem := strings.TrimSuffix(name, ".json")
		idx, err := strconv.ParseInt(stem, 10, 64)
		if err != nil {
			t.Fatalf("unexpected file in inclusion dir: %s", name)
		}
		onDisk++
		if !indexed[idx] {
			t.Fatalf("orphaned proof file %s not listed in index.json", name)
		}
	}
	if onDisk != secondIdx.Count {
		t.Fatalf("on-disk proofs=%d index count=%d", onDisk, secondIdx.Count)
	}
}

func TestSelectProofIndicesDeterministic(t *testing.T) {
	a := publish.SelectProofIndices(10000, 2000)
	b := publish.SelectProofIndices(10000, 2000)
	if len(a) != len(b) {
		t.Fatalf("len %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("mismatch at %d: %d vs %d", i, a[i], b[i])
		}
	}
	// Head and tail coverage.
	if a[0] != 0 {
		t.Fatal("expected index 0")
	}
	if a[len(a)-1] != 9999 {
		t.Fatal("expected last index")
	}
	seen := map[int64]bool{}
	for _, i := range a {
		seen[i] = true
	}
	for i := int64(0); i < 2000; i++ {
		if !seen[i] {
			t.Fatalf("missing head index %d", i)
		}
	}
	for i := int64(10000 - 500); i < 10000; i++ {
		if !seen[i] {
			t.Fatalf("missing tail index %d", i)
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
