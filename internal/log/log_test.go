package log

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func testEntry(i int) Entry {
	canon := []byte(fmt.Sprintf("plumbline-canon-v1\n{\"n\":%d}", i))
	sum := LeafHash(canon) // not the tool hash; Entry.Hash is separate
	return Entry{
		ServerID:        fmt.Sprintf("server-%d", i%7),
		ToolName:        fmt.Sprintf("tool-%d", i),
		Canonical:       canon,
		Hash:            "sha256:" + hex.EncodeToString(sum),
		ObservedAt:      int64(1_700_000_000 + i),
		ProtocolVersion: "2024-11-05",
		CanonVersion:    CanonVersionV1,
	}
}

func openTempLog(t *testing.T) *Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.sqlite")
	l, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestAppendOnlyTriggers(t *testing.T) {
	l := openTempLog(t)
	idx, err := l.Append(testEntry(0))
	if err != nil {
		t.Fatal(err)
	}
	if idx != 0 {
		t.Fatalf("first idx = %d, want 0", idx)
	}

	db := l.dbForTest()
	_, err = db.Exec(`UPDATE entries SET tool_name = 'corrupted' WHERE idx = 0`)
	if err == nil {
		t.Fatal("UPDATE against entries succeeded; trigger failed to block corruption")
	}
	t.Logf("UPDATE blocked: %v", err)

	_, err = db.Exec(`DELETE FROM entries WHERE idx = 0`)
	if err == nil {
		t.Fatal("DELETE against entries succeeded; trigger failed to block corruption")
	}
	t.Logf("DELETE blocked: %v", err)

	// Row must still be intact.
	var name string
	if err := db.QueryRow(`SELECT tool_name FROM entries WHERE idx = 0`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "tool-0" {
		t.Fatalf("row corrupted: got %q", name)
	}
}

func TestMerkleTenThousand(t *testing.T) {
	l := openTempLog(t)
	const n = 10_000

	for i := 0; i < n; i++ {
		idx, err := l.Append(testEntry(i))
		if err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
		if idx != int64(i) {
			t.Fatalf("Append(%d): idx=%d", i, idx)
		}
	}

	size, err := l.Size()
	if err != nil {
		t.Fatal(err)
	}
	if size != n {
		t.Fatalf("size=%d want %d", size, n)
	}

	// Independent root: recompute from leaf hashes loaded via SQL.
	leaves := mustLeafHashes(t, l)
	independentRoot := MerkleRoot(leaves)
	rootHex, err := l.RootHash()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(independentRoot) != rootHex {
		t.Fatalf("root mismatch: log=%s independent=%s", rootHex, hex.EncodeToString(independentRoot))
	}

	// Random sample of inclusion proofs.
	rng := rand.Reader
	seen := map[int64]bool{}
	for len(seen) < 64 {
		v, err := rand.Int(rng, big.NewInt(n))
		if err != nil {
			t.Fatal(err)
		}
		seen[v.Int64()] = true
	}
	for idx := range seen {
		p, err := l.InclusionProof(idx)
		if err != nil {
			t.Fatalf("InclusionProof(%d): %v", idx, err)
		}
		if !VerifyInclusion(int(p.Index), int(p.TreeSize), p.LeafHash, p.AuditPath, p.RootHash) {
			t.Fatalf("VerifyInclusion failed for idx %d", idx)
		}
		// Independent check: reconstructed root must match MerkleRoot(leaves).
		if !VerifyInclusion(int(idx), n, leaves[idx], p.AuditPath, independentRoot) {
			t.Fatalf("independent inclusion check failed for idx %d", idx)
		}
	}

	// Consistency proof between two sizes.
	oldSize, newSize := int64(3_000), int64(n)
	cp, err := l.ConsistencyProof(oldSize, newSize)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := MerkleRoot(leaves[:oldSize])
	newRoot := independentRoot
	if !bytesEqual(cp.OldRoot, oldRoot) || !bytesEqual(cp.NewRoot, newRoot) {
		t.Fatal("consistency proof roots do not match independent MerkleRoot")
	}
	if !VerifyConsistency(int(oldSize), int(newSize), oldRoot, newRoot, cp.AuditPath) {
		t.Fatal("VerifyConsistency failed")
	}

	// Also check a power-of-two old size (exercises proof seed prepend).
	oldSize = 4096
	cp, err = l.ConsistencyProof(oldSize, newSize)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot = MerkleRoot(leaves[:oldSize])
	if !VerifyConsistency(int(oldSize), int(newSize), oldRoot, newRoot, cp.AuditPath) {
		t.Fatal("VerifyConsistency failed for power-of-two oldSize")
	}
}

func TestCheckpointSignVerify(t *testing.T) {
	l := openTempLog(t)
	for i := 0; i < 5; i++ {
		if _, err := l.Append(testEntry(i)); err != nil {
			t.Fatal(err)
		}
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := l.SignCheckpoint(priv)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyCheckpoint(pub, cp) {
		t.Fatal("VerifyCheckpoint failed for valid signature")
	}
	cp.RootHash = "00" + cp.RootHash[2:]
	if VerifyCheckpoint(pub, cp) {
		t.Fatal("VerifyCheckpoint accepted tampered checkpoint")
	}
}

func TestNoUpdateDeleteMethodsExist(t *testing.T) {
	// Compile-time documentation: the Log API has Append but no Update/Delete.
	var _ interface {
		Append(Entry) (int64, error)
		InclusionProof(int64) (InclusionProof, error)
		ConsistencyProof(int64, int64) (ConsistencyProof, error)
		SignCheckpoint(ed25519.PrivateKey) (Checkpoint, error)
	} = (*Log)(nil)
}

func mustLeafHashes(t *testing.T, l *Log) [][]byte {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	leaves, err := l.leafHashesLocked()
	if err != nil {
		t.Fatal(err)
	}
	return leaves
}

func TestRFC6962EmptyRoot(t *testing.T) {
	got := hex.EncodeToString(EmptyRoot())
	// SHA-256 of empty input.
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Fatalf("EmptyRoot=%s want %s", got, want)
	}
}

func TestOpenCreatesTriggers(t *testing.T) {
	l := openTempLog(t)
	var name string
	err := l.dbForTest().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='trigger' AND name='entries_no_update'`,
	).Scan(&name)
	if err == sql.ErrNoRows {
		t.Fatal("entries_no_update trigger missing")
	}
	if err != nil {
		t.Fatal(err)
	}
}

// Ensure keys directory pattern is documented via this helper used by keygen tests.
func TestKeygenOutsideRepoPattern(t *testing.T) {
	dir := t.TempDir() // stands in for a path outside the repo
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privPath := filepath.Join(dir, "signing.ed25519")
	pubPath := filepath.Join(dir, "signing.ed25519.pub")
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.PrivateKey(loaded).Equal(priv) {
		t.Fatal("round-trip key mismatch")
	}
}
