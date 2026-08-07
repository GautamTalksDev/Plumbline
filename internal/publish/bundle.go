// Package publish writes a static signed-log bundle for the transparency site.
//
// It reads the Merkle log via the existing log package APIs and does not change
// what is recorded or how it is classified.
package publish

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// Frozen study version pins (must match the running study; do not invent new ones).
const (
	taxonomyVersion = "taxonomy-v1"
	eventsVersion   = "events-v2"
	cohortVersion   = "plumbline-cohort-v1"

	// DefaultMaxProofs is the default inclusion-proof sample budget and the
	// threshold used by the plumbline-proofsample-v1 selection rule.
	DefaultMaxProofs = 2000

	// TailProofs is how many of the newest leaf indices are always included
	// in the published inclusion-proof sample.
	TailProofs = 500

	// ProofSampleV1 is the domain-separation prefix for deterministic sampling.
	ProofSampleV1 = "plumbline-proofsample-v1|"
)

// Options controls Bundle.
type Options struct {
	LogPath   string
	OutDir    string // default site/log/
	Since     int64  // optional: only (re)write artifacts for idx >= Since (best-effort)
	Week      int    // study week N of 6; written into meta.json
	PubKey    ed25519.PublicKey
	MaxProofs int // inclusion proof sample budget (default DefaultMaxProofs)
}

// Meta is site/log/meta.json.
type Meta struct {
	TreeSize        int64  `json:"tree_size"`
	EntryCount      int64  `json:"entry_count"`
	LastRunDate     string `json:"last_run_date"`
	CanonVersion    string `json:"canon_version"`
	TaxonomyVersion string `json:"taxonomy_version"`
	EventsVersion   string `json:"events_version"`
	CohortVersion   string `json:"cohort_version"`
	Week            int    `json:"week"`
	WeekTotal       int    `json:"week_total"`
	CheckpointCount int    `json:"checkpoint_count"`
}

// CheckpointJSON is the public JSON shape for a signed tree head.
type CheckpointJSON struct {
	TreeSize  int64  `json:"tree_size"`
	RootHash  string `json:"root_hash"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}

// ConsistencyProofJSON is an RFC 6962 consistency proof between two sizes.
type ConsistencyProofJSON struct {
	OldSize   int64    `json:"old_size"`
	NewSize   int64    `json:"new_size"`
	OldRoot   string   `json:"old_root"`
	NewRoot   string   `json:"new_root"`
	AuditPath []string `json:"audit_path"`
}

// CheckpointsFile is site/log/checkpoints.json.
type CheckpointsFile struct {
	Checkpoints       []CheckpointJSON       `json:"checkpoints"`
	ConsistencyProofs []ConsistencyProofJSON `json:"consistency_proofs"`
}

// ServerIndex maps server id → shard filename + observation count.
type ServerIndex struct {
	Servers []ServerIndexEntry `json:"servers"`
}

// ServerIndexEntry is one row in servers/index.json.
type ServerIndexEntry struct {
	ID    string `json:"id"`
	File  string `json:"file"` // "ab.json" — first two hex digits of SHA-256(id)
	Count int    `json:"observation_count"`
}

// ServerObservation is hashes-and-metadata only (no tool descriptions).
type ServerObservation struct {
	Idx             int64  `json:"idx"`
	Date            string `json:"date"`
	Hash            string `json:"hash"`
	ProtocolVersion string `json:"protocol_version"`
	EventType       string `json:"event_type"`
}

// ServerShard is servers/<hh>.json — all servers whose id-hash starts with hh.
type ServerShard struct {
	Servers map[string]ServerShardEntry `json:"servers"`
}

// ServerShardEntry is one server inside a shard file.
type ServerShardEntry struct {
	Observations []ServerObservation `json:"observations"`
}

// InclusionProofJSON is site/log/proofs/inclusion/<idx>.json.
type InclusionProofJSON struct {
	Index     int64    `json:"index"`
	TreeSize  int64    `json:"tree_size"`
	LeafHash  string   `json:"leaf_hash"`
	AuditPath []string `json:"audit_path"`
	RootHash  string   `json:"root_hash"`
}

// InclusionIndex is proofs/inclusion/index.json.
type InclusionIndex struct {
	Count        int     `json:"count"`
	TotalEntries int64   `json:"total_entries"`
	MaxProofs    int     `json:"max_proofs"`
	Rule         string  `json:"rule"`
	Indices      []int64 `json:"indices"`
}

// Bundle writes a static log bundle under opt.OutDir.
func Bundle(opt Options) error {
	if opt.LogPath == "" {
		return fmt.Errorf("publish: --log is required")
	}
	out := opt.OutDir
	if out == "" {
		out = "site/log"
	}
	if len(opt.PubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("publish: --pubkey is required (32-byte Ed25519 public key)")
	}
	if opt.Week < 0 || opt.Week > 6 {
		return fmt.Errorf("publish: --week must be 0–6 (0 = pre-study / unknown)")
	}
	maxProofs := opt.MaxProofs
	if maxProofs <= 0 {
		maxProofs = DefaultMaxProofs
	}

	lg, err := log.Open(opt.LogPath)
	if err != nil {
		return err
	}
	defer func() { _ = lg.Close() }()

	size, err := lg.Size()
	if err != nil {
		return err
	}
	cps, err := lg.ListCheckpoints(1_000_000)
	if err != nil {
		return err
	}
	if len(cps) == 0 {
		return fmt.Errorf("publish: log has no signed checkpoints")
	}

	ents, err := lg.Entries(0, size)
	if err != nil {
		return err
	}

	leaves := make([][]byte, len(ents))
	for i, e := range ents {
		b, err := hex.DecodeString(e.LeafHash)
		if err != nil || len(b) != sha256.Size {
			return fmt.Errorf("publish: entry %d: bad leaf_hash", e.Idx)
		}
		leaves[i] = b
	}
	root := log.MerkleRoot(leaves)
	rootHex := hex.EncodeToString(root)

	serversDir := filepath.Join(out, "servers")
	incDir := filepath.Join(out, "proofs", "inclusion")
	consDir := filepath.Join(out, "proofs", "consistency")
	if err := os.MkdirAll(serversDir, 0o755); err != nil { //nolint:gosec // G301 -- public static site tree
		return err
	}
	if err := os.MkdirAll(incDir, 0o755); err != nil { //nolint:gosec // G301
		return err
	}
	if err := os.MkdirAll(consDir, 0o755); err != nil { //nolint:gosec // G301
		return err
	}

	fullPublish := opt.Since <= 0
	if fullPublish {
		// Wipe prior layout so a republish cannot leave orphaned proof/server
		// files from an older scheme (e.g. one-proof-per-entry).
		if err := resetLayoutDirs(out); err != nil {
			return err
		}
	}

	// pubkey.txt — hex, one line
	pubHex := hex.EncodeToString(opt.PubKey)
	if err := writeFile(filepath.Join(out, "pubkey.txt"), []byte(pubHex+"\n")); err != nil {
		return err
	}

	cpJSON := make([]CheckpointJSON, len(cps))
	for i, cp := range cps {
		cpJSON[i] = CheckpointJSON{
			TreeSize:  cp.TreeSize,
			RootHash:  cp.RootHash,
			Timestamp: cp.Timestamp,
			Signature: cp.Signature,
		}
		if !log.VerifyCheckpoint(opt.PubKey, cp) {
			return fmt.Errorf("publish: checkpoint at tree_size %d fails signature verification with provided pubkey", cp.TreeSize)
		}
	}
	latest := cpJSON[len(cpJSON)-1]
	if err := writeJSON(filepath.Join(out, "checkpoint.json"), latest); err != nil {
		return err
	}

	cons := make([]ConsistencyProofJSON, 0, len(cps)-1)
	for i := 1; i < len(cps); i++ {
		old := cps[i-1]
		newC := cps[i]
		proof, err := consistencyBetween(leaves, old.TreeSize, newC.TreeSize)
		if err != nil {
			return err
		}
		if proof.OldRoot != old.RootHash || proof.NewRoot != newC.RootHash {
			return fmt.Errorf("publish: consistency roots mismatch checkpoints %d→%d", old.TreeSize, newC.TreeSize)
		}
		cons = append(cons, proof)
		name := fmt.Sprintf("%d-%d.json", old.TreeSize, newC.TreeSize)
		if err := writeJSON(filepath.Join(consDir, name), proof); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(out, "checkpoints.json"), CheckpointsFile{
		Checkpoints:       cpJSON,
		ConsistencyProofs: cons,
	}); err != nil {
		return err
	}

	lastRun := lastRunDate(ents)
	meta := Meta{
		TreeSize:        size,
		EntryCount:      size,
		LastRunDate:     lastRun,
		CanonVersion:    log.CanonVersionV1,
		TaxonomyVersion: taxonomyVersion,
		EventsVersion:   eventsVersion,
		CohortVersion:   cohortVersion,
		Week:            opt.Week,
		WeekTotal:       6,
		CheckpointCount: len(cps),
	}
	if err := writeJSON(filepath.Join(out, "meta.json"), meta); err != nil {
		return err
	}

	if err := writeServerShards(serversDir, ents, fullPublish); err != nil {
		return err
	}
	if err := writeInclusionSample(incDir, leaves, size, rootHex, maxProofs, fullPublish, opt.Since); err != nil {
		return err
	}

	_ = latest
	return nil
}

func writeServerShards(serversDir string, ents []log.StoredEntry, fullPublish bool) error {
	byServer := map[string][]ServerObservation{}
	for _, e := range ents {
		byServer[e.ServerID] = append(byServer[e.ServerID], ServerObservation{
			Idx:             e.Idx,
			Date:            time.Unix(e.ObservedAt, 0).UTC().Format("2006-01-02"),
			Hash:            e.Hash,
			ProtocolVersion: e.ProtocolVersion,
			EventType:       eventType(e),
		})
	}
	ids := make([]string, 0, len(byServer))
	for id := range byServer {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	shards := map[string]*ServerShard{}
	for _, id := range ids {
		obs := byServer[id]
		file := serverShardName(id)
		sh := shards[file]
		if sh == nil {
			sh = &ServerShard{Servers: map[string]ServerShardEntry{}}
			shards[file] = sh
		}
		sh.Servers[id] = ServerShardEntry{Observations: obs}
	}
	for file, sh := range shards {
		if err := writeJSON(filepath.Join(serversDir, file), sh); err != nil {
			return err
		}
	}
	// Full publish already reset the directory. Incremental leaves orphans on
	// disk by design; index always reflects shards that actually exist and
	// the servers we just wrote into them.
	return writeServerIndex(serversDir, ids, byServer)
}

func writeServerIndex(serversDir string, ids []string, byServer map[string][]ServerObservation) error {
	index := ServerIndex{Servers: make([]ServerIndexEntry, 0, len(ids))}
	for _, id := range ids {
		file := serverShardName(id)
		// Only list servers whose shard file is present.
		if _, err := os.Stat(filepath.Join(serversDir, file)); err != nil {
			continue
		}
		index.Servers = append(index.Servers, ServerIndexEntry{
			ID:    id,
			File:  file,
			Count: len(byServer[id]),
		})
	}
	return writeJSON(filepath.Join(serversDir, "index.json"), index)
}

func writeInclusionSample(incDir string, leaves [][]byte, treeSize int64, rootHex string, maxProofs int, fullPublish bool, since int64) error {
	indices := SelectProofIndices(treeSize, maxProofs)
	for _, idx := range indices {
		if !fullPublish && since > 0 && idx < since {
			continue
		}
		i := int(idx)
		if i < 0 || i >= len(leaves) {
			return fmt.Errorf("publish: sampled index %d out of range (tree_size=%d)", idx, treeSize)
		}
		path, err := log.InclusionAuditPath(i, leaves)
		if err != nil {
			return fmt.Errorf("publish: inclusion %d: %w", i, err)
		}
		pj := InclusionProofJSON{
			Index:     idx,
			TreeSize:  treeSize,
			LeafHash:  hex.EncodeToString(leaves[i]),
			AuditPath: pathHex(path),
			RootHash:  rootHex,
		}
		if err := writeJSON(filepath.Join(incDir, fmt.Sprintf("%d.json", idx)), pj); err != nil {
			return err
		}
	}
	// Index lists proofs that actually exist on disk (full publish: exactly
	// the sample; --since: sample union prior leftovers).
	return writeInclusionIndexFromDisk(incDir, treeSize, maxProofs)
}

func writeInclusionIndexFromDisk(incDir string, treeSize int64, maxProofs int) error {
	entries, err := os.ReadDir(incDir)
	if err != nil {
		return err
	}
	var indices []int64
	for _, e := range entries {
		if e.IsDir() || !isInclusionProofFile(e.Name()) {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".json")
		idx, err := strconv.ParseInt(stem, 10, 64)
		if err != nil || idx < 0 {
			continue
		}
		indices = append(indices, idx)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
	return writeJSON(filepath.Join(incDir, "index.json"), InclusionIndex{
		Count:        len(indices),
		TotalEntries: treeSize,
		MaxProofs:    maxProofs,
		Rule:         "plumbline-proofsample-v1",
		Indices:      indices,
	})
}

// resetLayoutDirs removes bundle layout files under out/proofs and out/servers.
// It never deletes outside out, never removes out itself, and only matches
// known publish-bundle filenames (never a blind rm -rf).
func resetLayoutDirs(out string) error {
	absOut, err := filepath.Abs(out)
	if err != nil {
		return fmt.Errorf("publish: resolve --out: %w", err)
	}
	if absOut == "" || absOut == string(filepath.Separator) {
		return fmt.Errorf("publish: refusing to reset unsafe --out %q", out)
	}
	type target struct {
		rel   string
		match func(string) bool
	}
	targets := []target{
		{"proofs/inclusion", isInclusionLayoutFile},
		{"proofs/consistency", isConsistencyLayoutFile},
		{"servers", isServerLayoutFile},
	}
	for _, t := range targets {
		dir := filepath.Join(absOut, filepath.FromSlash(t.rel))
		if !pathInside(absOut, dir) {
			return fmt.Errorf("publish: refusing to clear %q (outside --out)", dir)
		}
		if err := clearDirMatching(dir, t.match); err != nil {
			return fmt.Errorf("publish: clear %s: %w", t.rel, err)
		}
	}
	return nil
}

func pathInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func isInclusionProofFile(name string) bool {
	if !strings.HasSuffix(name, ".json") || name == "index.json" {
		return false
	}
	stem := strings.TrimSuffix(name, ".json")
	_, err := strconv.ParseInt(stem, 10, 64)
	return err == nil
}

func isInclusionLayoutFile(name string) bool {
	return name == "index.json" || isInclusionProofFile(name)
}

func isConsistencyLayoutFile(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	stem := strings.TrimSuffix(name, ".json")
	parts := strings.Split(stem, "-")
	if len(parts) != 2 {
		return false
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return false
	}
	_, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil
}

func isServerLayoutFile(name string) bool {
	if name == "index.json" {
		return true
	}
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	stem := strings.TrimSuffix(name, ".json")
	// Current shards: two hex digits. Legacy: full SHA-256 hex of server id.
	if len(stem) == 2 || len(stem) == 64 {
		for _, c := range stem {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
		return true
	}
	return false
}

// SelectProofIndices returns the sorted leaf indices for which inclusion
// proofs are published under plumbline-proofsample-v1.
//
// Include index i (0 ≤ i < treeSize) iff any of:
//  1. i < maxProofs
//  2. i is among the last min(TailProofs, treeSize) indices
//  3. uint64(SHA-256("plumbline-proofsample-v1|" + decimal(i))[:8]) % treeSize < maxProofs
//
// When treeSize ≤ maxProofs, every index is included.
func SelectProofIndices(treeSize int64, maxProofs int) []int64 {
	if treeSize <= 0 {
		return nil
	}
	if maxProofs <= 0 {
		maxProofs = DefaultMaxProofs
	}
	if treeSize <= int64(maxProofs) {
		out := make([]int64, treeSize)
		for i := int64(0); i < treeSize; i++ {
			out[i] = i
		}
		return out
	}

	selected := make(map[int64]struct{}, maxProofs+TailProofs)
	for i := int64(0); i < int64(maxProofs) && i < treeSize; i++ {
		selected[i] = struct{}{}
	}
	tail := int64(TailProofs)
	if tail > treeSize {
		tail = treeSize
	}
	for i := treeSize - tail; i < treeSize; i++ {
		selected[i] = struct{}{}
	}
	mod := uint64(treeSize) //nolint:gosec // G115 -- treeSize > 0
	if maxProofs < 0 {
		maxProofs = DefaultMaxProofs
	}
	threshold := uint64(maxProofs) //nolint:gosec // G115 -- non-negative after clamp
	for i := int64(0); i < treeSize; i++ {
		sum := sha256.Sum256([]byte(ProofSampleV1 + strconv.FormatInt(i, 10)))
		n := binary.BigEndian.Uint64(sum[:8])
		if n%mod < threshold {
			selected[i] = struct{}{}
		}
	}
	out := make([]int64, 0, len(selected))
	for i := range selected {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

func consistencyBetween(leaves [][]byte, oldSize, newSize int64) (ConsistencyProofJSON, error) {
	if newSize < 0 || newSize > int64(len(leaves)) {
		return ConsistencyProofJSON{}, fmt.Errorf("publish: bad newSize %d", newSize)
	}
	if oldSize < 0 || oldSize > newSize {
		return ConsistencyProofJSON{}, fmt.Errorf("publish: bad sizes %d→%d", oldSize, newSize)
	}
	prefix := leaves[:newSize]
	path, err := log.ConsistencyAuditPath(int(oldSize), int(newSize), prefix)
	if err != nil {
		return ConsistencyProofJSON{}, err
	}
	var oldRoot []byte
	if oldSize == 0 {
		oldRoot = log.EmptyRoot()
	} else {
		oldRoot = log.MerkleRoot(prefix[:oldSize])
	}
	newRoot := log.MerkleRoot(prefix)
	return ConsistencyProofJSON{
		OldSize:   oldSize,
		NewSize:   newSize,
		OldRoot:   hex.EncodeToString(oldRoot),
		NewRoot:   hex.EncodeToString(newRoot),
		AuditPath: pathHex(path),
	}, nil
}

func pathHex(path [][]byte) []string {
	out := make([]string, len(path))
	for i, h := range path {
		out[i] = hex.EncodeToString(h)
	}
	return out
}

func serverShardName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:1]) + ".json"
}

func eventType(e log.StoredEntry) string {
	payload := e.Canonical
	if i := bytes.IndexByte(payload, '\n'); i >= 0 {
		payload = payload[i+1:]
	}
	var m struct {
		Event string `json:"event"`
	}
	if json.Unmarshal(payload, &m) == nil && m.Event != "" {
		return m.Event
	}
	switch {
	case strings.HasPrefix(e.ToolName, "_crawl_status"):
		return "status"
	case strings.HasPrefix(e.ToolName, "_version_poll"):
		return "version_poll"
	default:
		return "tool"
	}
}

func lastRunDate(ents []log.StoredEntry) string {
	var max int64
	for _, e := range ents {
		if e.ObservedAt > max {
			max = e.ObservedAt
		}
	}
	if max == 0 {
		return ""
	}
	return time.Unix(max, 0).UTC().Format("2006-01-02")
}

func clearDirMatching(dir string, match func(name string) bool) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !match(e.Name()) {
			continue
		}
		p := filepath.Join(absDir, e.Name())
		if !pathInside(absDir, p) {
			return fmt.Errorf("publish: refusing to remove %q", p)
		}
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFile(path, b)
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301
		return err
	}
	return os.WriteFile(path, b, 0o644) //nolint:gosec // G306 — public static artifacts
}

// CountBundleFiles returns the number of regular files under dir (recursive).
func CountBundleFiles(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}
