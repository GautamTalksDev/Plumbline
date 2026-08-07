package verify

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// BundleOptions controls VerifyBundle.
type BundleOptions struct {
	// Bundle is a filesystem path or http(s) URL to the static log directory
	// (the directory that contains checkpoints.json and pubkey.txt).
	Bundle     string
	PublicKey  ed25519.PublicKey // optional; otherwise read from bundle pubkey.txt
	HTTPClient *http.Client
	// MaxEntries caps how many inclusion proofs to sample (0 = 500).
	MaxEntries int
}

// checkpointWire is the JSON shape written by publish-bundle / plumbd.
type checkpointWire struct {
	TreeSize  int64  `json:"tree_size"`
	RootHash  string `json:"root_hash"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}

type checkpointsFile struct {
	Checkpoints       []checkpointWire       `json:"checkpoints"`
	ConsistencyProofs []consistencyProofWire `json:"consistency_proofs"`
}

type consistencyProofWire struct {
	OldSize   int64    `json:"old_size"`
	NewSize   int64    `json:"new_size"`
	OldRoot   string   `json:"old_root"`
	NewRoot   string   `json:"new_root"`
	AuditPath []string `json:"audit_path"`
}

type inclusionProofWire struct {
	Index     int64    `json:"index"`
	TreeSize  int64    `json:"tree_size"`
	LeafHash  string   `json:"leaf_hash"`
	AuditPath []string `json:"audit_path"`
	RootHash  string   `json:"root_hash"`
}

// VerifyBundle verifies every Ed25519 signature in checkpoints.json, consistency
// proofs between consecutive checkpoints, and a bounded random sample of
// inclusion proofs under proofs/inclusion/. It reuses log.VerifyCheckpoint,
// log.VerifyConsistency, and log.VerifyInclusion without modifying them.
func VerifyBundle(ctx context.Context, opt BundleOptions) (Result, error) {
	base := strings.TrimRight(opt.Bundle, "/")
	res := Result{Endpoint: base}
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
		raw, err := fetchBytes(ctx, client, base, "pubkey.txt")
		if err != nil {
			return fail("pubkey: " + err.Error())
		}
		b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return fail("pubkey: invalid hex")
		}
		pub = ed25519.PublicKey(b)
	}
	res.PubkeyHex = hex.EncodeToString(pub)

	raw, err := fetchBytes(ctx, client, base, "checkpoints.json")
	if err != nil {
		return fail("checkpoints.json: " + err.Error())
	}
	var file checkpointsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return fail("checkpoints.json: " + err.Error())
	}
	if len(file.Checkpoints) == 0 {
		return fail("checkpoints.json: empty")
	}

	// Prefer checkpoint.json for the signed tree head used in inclusion checks.
	var signedHead checkpointWire
	if cpRaw, err := fetchBytes(ctx, client, base, "checkpoint.json"); err == nil {
		if err := json.Unmarshal(cpRaw, &signedHead); err != nil {
			return fail("checkpoint.json: " + err.Error())
		}
	} else {
		signedHead = file.Checkpoints[len(file.Checkpoints)-1]
	}
	res.TreeSize = signedHead.TreeSize
	res.RootHash = signedHead.RootHash

	for i, cp := range file.Checkpoints {
		ok := log.VerifyCheckpoint(pub, log.Checkpoint{
			TreeSize:  cp.TreeSize,
			RootHash:  cp.RootHash,
			Timestamp: cp.Timestamp,
			Signature: cp.Signature,
		})
		if !ok {
			return fail(fmt.Sprintf("checkpoint signature verification failed at tree_size %d (checkpoint index %d) — log may be tampered or key mismatch", cp.TreeSize, i))
		}
	}
	if !log.VerifyCheckpoint(pub, log.Checkpoint{
		TreeSize:  signedHead.TreeSize,
		RootHash:  signedHead.RootHash,
		Timestamp: signedHead.Timestamp,
		Signature: signedHead.Signature,
	}) {
		return fail(fmt.Sprintf("checkpoint.json signature verification failed at tree_size %d", signedHead.TreeSize))
	}
	res.CheckpointOK = true

	// Consistency between consecutive checkpoints.
	res.ConsistencyOK = true
	proofByPair := map[string]consistencyProofWire{}
	for _, p := range file.ConsistencyProofs {
		key := fmt.Sprintf("%d:%d", p.OldSize, p.NewSize)
		proofByPair[key] = p
	}
	for i := 1; i < len(file.Checkpoints); i++ {
		old := file.Checkpoints[i-1]
		newC := file.Checkpoints[i]
		key := fmt.Sprintf("%d:%d", old.TreeSize, newC.TreeSize)
		p, ok := proofByPair[key]
		if !ok {
			var pf consistencyProofWire
			name := fmt.Sprintf("proofs/consistency/%d-%d.json", old.TreeSize, newC.TreeSize)
			b, err := fetchBytes(ctx, client, base, name)
			if err != nil {
				res.ConsistencyOK = false
				return fail(fmt.Sprintf("consistency proof missing for checkpoints %d→%d: %v", old.TreeSize, newC.TreeSize, err))
			}
			if err := json.Unmarshal(b, &pf); err != nil {
				res.ConsistencyOK = false
				return fail(fmt.Sprintf("consistency proof for checkpoints %d→%d: %v", old.TreeSize, newC.TreeSize, err))
			}
			p = pf
		}
		oldRoot, err1 := hex.DecodeString(p.OldRoot)
		newRoot, err2 := hex.DecodeString(p.NewRoot)
		if err1 != nil || err2 != nil {
			res.ConsistencyOK = false
			return fail(fmt.Sprintf("consistency proof for checkpoints %d→%d: bad root hex", old.TreeSize, newC.TreeSize))
		}
		wantOld, _ := hex.DecodeString(old.RootHash)
		wantNew, _ := hex.DecodeString(newC.RootHash)
		if !bytesEq(oldRoot, wantOld) || !bytesEq(newRoot, wantNew) {
			res.ConsistencyOK = false
			return fail(fmt.Sprintf("consistency proof roots do not match checkpoints at tree_size %d→%d", old.TreeSize, newC.TreeSize))
		}
		path, err := decodePath(p.AuditPath)
		if err != nil {
			res.ConsistencyOK = false
			return fail(fmt.Sprintf("consistency proof for checkpoints %d→%d: %v", old.TreeSize, newC.TreeSize, err))
		}
		if !log.VerifyConsistency(int(old.TreeSize), int(newC.TreeSize), oldRoot, newRoot, path) {
			res.ConsistencyOK = false
			return fail(fmt.Sprintf("consistency proof failed between checkpoints at tree_size %d and %d", old.TreeSize, newC.TreeSize))
		}
	}

	signedRoot, err := hex.DecodeString(signedHead.RootHash)
	if err != nil {
		return fail("checkpoint.json root_hash: invalid hex")
	}

	indices, skipReason, err := listInclusionIndices(ctx, client, base)
	if err != nil {
		return fail("inclusion proofs: " + err.Error())
	}
	res.InclusionProofs = len(indices)

	if skipReason != "" || len(indices) == 0 {
		// Inclusion was not performed. Never report true.
		res.InclusionOK = false
		res.InclusionSkipped = true
		if skipReason == "" {
			skipReason = "0 proofs found"
		}
		res.InclusionSkipReason = skipReason
		res.CheckedEntries = 0
		res.EntriesHashedOK = false
		res.EntriesHashedSkipped = true
		res.OK = res.CheckpointOK && res.ConsistencyOK && len(res.Errors) == 0
		return res, nil
	}

	sample := sampleIndices(indices, maxEnt)
	if len(sample) == 0 {
		// Proofs exist but none could be checked — FAIL.
		res.InclusionOK = false
		res.CheckedEntries = 0
		res.EntriesHashedOK = false
		res.EntriesHashedSkipped = true
		return fail(fmt.Sprintf("inclusion: %d proofs present but 0 checked (max-entries=%d) — refusing false assurance", len(indices), maxEnt))
	}

	res.InclusionOK = true
	for _, idx := range sample {
		name := fmt.Sprintf("proofs/inclusion/%d.json", idx)
		b, err := fetchBytes(ctx, client, base, name)
		if err != nil {
			res.InclusionOK = false
			if res.CheckedEntries == 0 {
				res.EntriesHashedSkipped = true
			}
			return fail(fmt.Sprintf("inclusion proof for idx %d: %v", idx, err))
		}
		var proof inclusionProofWire
		if err := json.Unmarshal(b, &proof); err != nil {
			res.InclusionOK = false
			if res.CheckedEntries == 0 {
				res.EntriesHashedSkipped = true
			}
			return fail(fmt.Sprintf("inclusion proof for idx %d: %v", idx, err))
		}
		if proof.Index != idx {
			res.InclusionOK = false
			if res.CheckedEntries == 0 {
				res.EntriesHashedSkipped = true
			}
			return fail(fmt.Sprintf("inclusion proof for idx %d: file claims index %d", idx, proof.Index))
		}
		leaf, err := hex.DecodeString(proof.LeafHash)
		if err != nil {
			res.InclusionOK = false
			if res.CheckedEntries == 0 {
				res.EntriesHashedSkipped = true
			}
			return fail(fmt.Sprintf("inclusion proof for idx %d: bad leaf_hash", idx))
		}
		path, err := decodePath(proof.AuditPath)
		if err != nil {
			res.InclusionOK = false
			if res.CheckedEntries == 0 {
				res.EntriesHashedSkipped = true
			}
			return fail(fmt.Sprintf("inclusion proof for idx %d: %v", idx, err))
		}
		proofRoot, err := hex.DecodeString(proof.RootHash)
		if err != nil {
			res.InclusionOK = false
			if res.CheckedEntries == 0 {
				res.EntriesHashedSkipped = true
			}
			return fail(fmt.Sprintf("inclusion proof for idx %d: bad root_hash", idx))
		}
		if !bytesEq(proofRoot, signedRoot) {
			res.InclusionOK = false
			if res.CheckedEntries == 0 {
				res.EntriesHashedSkipped = true
			}
			return fail(fmt.Sprintf("inclusion proof for idx %d: proof root != signed checkpoint root", idx))
		}
		treeSize := int(proof.TreeSize)
		if treeSize == 0 {
			treeSize = int(signedHead.TreeSize)
		}
		if !log.VerifyInclusion(int(proof.Index), treeSize, leaf, path, signedRoot) {
			res.InclusionOK = false
			// Leaf hash was evaluated via inclusion and failed.
			res.EntriesHashedOK = false
			res.EntriesHashedSkipped = false
			return fail(fmt.Sprintf("inclusion proof failed for idx %d — tampering detected", idx))
		}
		res.CheckedEntries++
	}

	// Invariant: never report inclusion success with a zero checked count when proofs exist.
	if res.InclusionOK && res.CheckedEntries == 0 {
		res.InclusionOK = false
		res.EntriesHashedSkipped = true
		return fail(fmt.Sprintf("inclusion: internal error — %d proofs present but checked count is 0", len(indices)))
	}

	// Bundle path: leaf hashes in the sampled proofs are bound to the signed
	// root by VerifyInclusion. That is the evaluation — not a separate pass.
	res.EntriesHashedOK = true
	res.EntriesHashedSkipped = false
	res.OK = res.CheckpointOK && res.ConsistencyOK && res.InclusionOK && len(res.Errors) == 0
	return res, nil
}

// listInclusionIndices enumerates which inclusion proofs the bundle contains.
// Local paths use proofs/inclusion/ readdir. HTTP(S) requires
// proofs/inclusion/index.json and never fabricates a 0..tree_size range.
// skipReason is non-empty when inclusion should be reported as skipped.
func listInclusionIndices(ctx context.Context, client *http.Client, base string) (indices []int64, skipReason string, err error) {
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		return listInclusionIndicesHTTP(ctx, client, base)
	}
	dir := filepath.Join(base, "proofs", "inclusion")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "0 proofs found", nil
		}
		return nil, "", err
	}
	out, err := indicesFromDirEntries(entries)
	if err != nil {
		return nil, "", err
	}
	if len(out) == 0 {
		return nil, "0 proofs found", nil
	}
	return out, "", nil
}

func indicesFromDirEntries(entries []os.DirEntry) ([]int64, error) {
	var out []int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "index.json" {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		stem := strings.TrimSuffix(name, ".json")
		idx, err := strconv.ParseInt(stem, 10, 64)
		if err != nil || idx < 0 {
			continue
		}
		out = append(out, idx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func listInclusionIndicesHTTP(ctx context.Context, client *http.Client, base string) ([]int64, string, error) {
	raw, err := fetchBytes(ctx, client, base, "proofs/inclusion/index.json")
	if err != nil {
		// Absent index: never invent 0..tree_size. Report skipped.
		return nil, "no proof index", nil
	}
	var idxFile struct {
		Indices []int64 `json:"indices"`
		Count   int     `json:"count"`
	}
	if err := json.Unmarshal(raw, &idxFile); err != nil {
		return nil, "", fmt.Errorf("proofs/inclusion/index.json: %w", err)
	}
	// Only the explicit indices list is authoritative. Never expand count into
	// a fabricated contiguous range (proofs may be a sparse subset of the tree).
	if len(idxFile.Indices) == 0 {
		return nil, "0 proofs found", nil
	}
	out := append([]int64(nil), idxFile.Indices...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if idxFile.Count > 0 && idxFile.Count != len(out) {
		// Prefer indices; count is informational. Mismatch is not fatal but
		// use the list length as InclusionProofs via caller.
		_ = idxFile.Count
	}
	return out, "", nil
}

// sampleIndices returns a random subset of indices of size min(n, maxN).
func sampleIndices(indices []int64, maxN int) []int64 {
	if maxN <= 0 || len(indices) == 0 {
		return nil
	}
	if len(indices) <= maxN {
		out := append([]int64(nil), indices...)
		return out
	}
	// Partial Fisher–Yates using crypto/rand.
	buf := append([]int64(nil), indices...)
	for i := 0; i < maxN; i++ {
		j, err := randInt(len(buf) - i)
		if err != nil {
			// Deterministic fallback: first maxN (still non-empty).
			return buf[:maxN]
		}
		j += i
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf[:maxN]
}

func randInt(n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("n <= 0")
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

func fetchBytes(ctx context.Context, client *http.Client, base, rel string) ([]byte, error) {
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		u, err := url.JoinPath(base, rel)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("User-Agent", "plumb-verify-log/0.1")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rel)
		}
		return body, nil
	}
	p := filepath.Join(base, filepath.FromSlash(rel))
	return os.ReadFile(p) //nolint:gosec // G304 — operator-supplied bundle path
}
