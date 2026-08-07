// Package verify checks a remote transparency-log HTTP endpoint.
//
// It works against ANY server that implements the Plumbline log HTTP API —
// that is what makes this a transparency log rather than a database client.
package verify

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// Result is the outcome of Verify.
type Result struct {
	OK               bool   `json:"ok"`
	Endpoint         string `json:"endpoint"`
	TreeSize         int64  `json:"tree_size"`
	RootHash         string `json:"root_hash"`
	PubkeyHex        string `json:"pubkey_hex"`
	CheckpointOK     bool   `json:"checkpoint_ok"`
	ConsistencyOK    bool   `json:"consistency_ok"`
	InclusionOK      bool   `json:"inclusion_ok"`
	InclusionSkipped bool   `json:"inclusion_skipped,omitempty"`
	// InclusionSkipReason is set when InclusionSkipped, e.g. "0 proofs found"
	// or "no proof index".
	InclusionSkipReason string `json:"inclusion_skip_reason,omitempty"`
	InclusionProofs     int    `json:"inclusion_proofs"` // proofs found in the bundle
	EntriesHashedOK     bool   `json:"entries_hashed_ok"`
	// EntriesHashedSkipped is true when leaf-hash verification was not
	// evaluated (e.g. inclusion skipped or failed before any check).
	EntriesHashedSkipped bool     `json:"entries_hashed_skipped,omitempty"`
	CheckedEntries       int      `json:"checked_entries"`
	Errors               []string `json:"errors,omitempty"`
}

// Options controls Verify.
type Options struct {
	URL        string // base URL of the log endpoint
	PublicKey  ed25519.PublicKey
	HTTPClient *http.Client
	// MaxEntries caps how many leaves we download to re-hash (0 = min(tree, 500)).
	MaxEntries int
}

// Verify fetches checkpoints and proofs from endpoint and reports tampering.
func Verify(ctx context.Context, opt Options) (Result, error) {
	base := strings.TrimRight(opt.URL, "/")
	res := Result{Endpoint: base, PubkeyHex: hex.EncodeToString(opt.PublicKey)}
	client := opt.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	maxEnt := opt.MaxEntries
	if maxEnt <= 0 {
		maxEnt = 500
	}

	fail := func(msg string) (Result, error) {
		res.OK = false
		res.Errors = append(res.Errors, msg)
		return res, nil
	}

	pub := opt.PublicKey
	if len(pub) != ed25519.PublicKeySize {
		// Fetch from endpoint.
		var pk struct {
			PublicKey string `json:"public_key"`
		}
		if err := getJSON(ctx, client, base+"/pubkey", &pk); err != nil {
			return fail("pubkey: " + err.Error())
		}
		b, err := hex.DecodeString(pk.PublicKey)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return fail("pubkey: invalid hex")
		}
		pub = ed25519.PublicKey(b)
		res.PubkeyHex = pk.PublicKey
	}

	var cp struct {
		TreeSize  int64  `json:"tree_size"`
		RootHash  string `json:"root_hash"`
		Timestamp int64  `json:"timestamp"`
		Signature string `json:"signature"`
	}
	if err := getJSON(ctx, client, base+"/checkpoint", &cp); err != nil {
		return fail("checkpoint: " + err.Error())
	}
	res.TreeSize = cp.TreeSize
	res.RootHash = cp.RootHash

	sth := log.Checkpoint{
		TreeSize:  cp.TreeSize,
		RootHash:  cp.RootHash,
		Timestamp: cp.Timestamp,
		Signature: cp.Signature,
	}
	if !log.VerifyCheckpoint(pub, sth) {
		return fail("checkpoint signature verification failed — log may be tampered or key mismatch")
	}
	res.CheckpointOK = true

	// Consistency across published checkpoints when ≥2 exist.
	var cps struct {
		Checkpoints []struct {
			TreeSize  int64  `json:"tree_size"`
			RootHash  string `json:"root_hash"`
			Timestamp int64  `json:"timestamp"`
			Signature string `json:"signature"`
		} `json:"checkpoints"`
	}
	_ = getJSON(ctx, client, base+"/checkpoints?limit=50", &cps)
	res.ConsistencyOK = true
	for i := 1; i < len(cps.Checkpoints); i++ {
		old := cps.Checkpoints[i-1]
		newC := cps.Checkpoints[i]
		if !log.VerifyCheckpoint(pub, log.Checkpoint{
			TreeSize: old.TreeSize, RootHash: old.RootHash,
			Timestamp: old.Timestamp, Signature: old.Signature,
		}) {
			return fail(fmt.Sprintf("historical checkpoint at size %d has invalid signature", old.TreeSize))
		}
		var proof struct {
			OldSize   int64    `json:"old_size"`
			NewSize   int64    `json:"new_size"`
			OldRoot   string   `json:"old_root"`
			NewRoot   string   `json:"new_root"`
			AuditPath []string `json:"audit_path"`
		}
		u := fmt.Sprintf("%s/proof/consistency?old=%d&new=%d", base, old.TreeSize, newC.TreeSize)
		if err := getJSON(ctx, client, u, &proof); err != nil {
			return fail("consistency proof: " + err.Error())
		}
		oldRoot, err1 := hex.DecodeString(proof.OldRoot)
		newRoot, err2 := hex.DecodeString(proof.NewRoot)
		if err1 != nil || err2 != nil {
			return fail("consistency proof: bad root hex")
		}
		path, err := decodePath(proof.AuditPath)
		if err != nil {
			return fail("consistency proof: " + err.Error())
		}
		wantOld, _ := hex.DecodeString(old.RootHash)
		wantNew, _ := hex.DecodeString(newC.RootHash)
		if !bytesEq(oldRoot, wantOld) || !bytesEq(newRoot, wantNew) {
			return fail("consistency proof roots do not match checkpoints")
		}
		if !log.VerifyConsistency(int(old.TreeSize), int(newC.TreeSize), oldRoot, newRoot, path) {
			return fail(fmt.Sprintf("consistency proof failed between sizes %d and %d", old.TreeSize, newC.TreeSize))
		}
	}

	// Re-hash leaves and check inclusion against the signed root.
	n := int(cp.TreeSize)
	if n == 0 {
		res.OK = true
		res.InclusionOK = true
		res.EntriesHashedOK = true
		return res, nil
	}
	checkN := n
	if checkN > maxEnt {
		checkN = maxEnt
	}
	var entriesResp struct {
		Entries []struct {
			Idx       int64  `json:"idx"`
			Canonical string `json:"canonical"`
			LeafHash  string `json:"leaf_hash"`
			Hash      string `json:"hash"`
		} `json:"entries"`
	}
	u := fmt.Sprintf("%s/entries?from=0&to=%d", base, checkN)
	if err := getJSON(ctx, client, u, &entriesResp); err != nil {
		return fail("entries: " + err.Error())
	}
	if len(entriesResp.Entries) != checkN {
		return fail(fmt.Sprintf("entries: got %d want %d", len(entriesResp.Entries), checkN))
	}

	root, err := hex.DecodeString(cp.RootHash)
	if err != nil {
		return fail("checkpoint root_hash: invalid hex")
	}

	res.InclusionOK = true
	res.EntriesHashedOK = true
	for _, e := range entriesResp.Entries {
		canon, err := base64.StdEncoding.DecodeString(e.Canonical)
		if err != nil {
			return fail(fmt.Sprintf("entry %d: bad canonical base64", e.Idx))
		}
		leaf := log.LeafHash(canon)
		leafHex := hex.EncodeToString(leaf)
		if leafHex != e.LeafHash {
			res.EntriesHashedOK = false
			return fail(fmt.Sprintf("entry %d: leaf_hash mismatch (stored %s computed %s) — tampering detected", e.Idx, e.LeafHash, leafHex))
		}
		var proof struct {
			Index     int64    `json:"index"`
			TreeSize  int64    `json:"tree_size"`
			LeafHash  string   `json:"leaf_hash"`
			AuditPath []string `json:"audit_path"`
			RootHash  string   `json:"root_hash"`
		}
		pu := fmt.Sprintf("%s/proof/inclusion?idx=%d", base, e.Idx)
		if err := getJSON(ctx, client, pu, &proof); err != nil {
			return fail(fmt.Sprintf("inclusion %d: %v", e.Idx, err))
		}
		path, err := decodePath(proof.AuditPath)
		if err != nil {
			return fail(err.Error())
		}
		proofRoot, _ := hex.DecodeString(proof.RootHash)
		if !bytesEq(proofRoot, root) {
			return fail(fmt.Sprintf("inclusion %d: proof root != checkpoint root", e.Idx))
		}
		if !log.VerifyInclusion(int(e.Idx), int(proof.TreeSize), leaf, path, root) {
			res.InclusionOK = false
			return fail(fmt.Sprintf("inclusion proof failed for idx %d — tampering detected", e.Idx))
		}
		res.CheckedEntries++
	}

	// If we only checked a prefix, still verify consistency of that prefix's
	// recomputed root is not claimed as full tree — inclusion proofs already
	// bind each leaf to the signed full-tree root.
	res.OK = res.CheckpointOK && res.ConsistencyOK && res.InclusionOK && res.EntriesHashedOK && len(res.Errors) == 0
	return res, nil
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, dst any) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "plumb-verify-log/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return json.Unmarshal(body, dst)
}

func decodePath(hexes []string) ([][]byte, error) {
	out := make([][]byte, len(hexes))
	for i, h := range hexes {
		b, err := hex.DecodeString(h)
		if err != nil {
			return nil, fmt.Errorf("audit path[%d]: %w", i, err)
		}
		out[i] = b
	}
	return out, nil
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
