// Package publish writes a static signed-log bundle for the transparency site.
//
// It reads the Merkle log via the existing log package APIs and does not change
// what is recorded or how it is classified.
package publish

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/log"
)

// Frozen study version pins (must match the running study; do not invent new ones).
const (
	taxonomyVersion = "taxonomy-v1"
	eventsVersion   = "events-v2"
	cohortVersion   = "plumbline-cohort-v1"
)

// Options controls Bundle.
type Options struct {
	LogPath string
	OutDir  string // default site/log/
	Since   int64  // optional: only (re)write per-server / inclusion artifacts for idx >= Since
	Week    int    // study week N of 6; written into meta.json
	PubKey  ed25519.PublicKey
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

// ServerIndex maps server id → filename (sha256 hex of id) + observation count.
type ServerIndex struct {
	Servers []ServerIndexEntry `json:"servers"`
}

// ServerIndexEntry is one row in servers/index.json.
type ServerIndexEntry struct {
	ID    string `json:"id"`
	File  string `json:"file"`
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

// ServerHistory is site/log/servers/<sha256>.json.
type ServerHistory struct {
	ID           string              `json:"id"`
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

	lg, err := log.Open(opt.LogPath)
	if err != nil {
		return err
	}
	defer lg.Close()

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

	if err := os.MkdirAll(filepath.Join(out, "servers"), 0o755); err != nil { //nolint:gosec // G301 -- public static site tree
		return err
	}
	if err := os.MkdirAll(filepath.Join(out, "proofs", "inclusion"), 0o755); err != nil { //nolint:gosec // G301
		return err
	}
	if err := os.MkdirAll(filepath.Join(out, "proofs", "consistency"), 0o755); err != nil { //nolint:gosec // G301
		return err
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
		// Bind proof roots to the signed checkpoint hashes.
		if proof.OldRoot != old.RootHash || proof.NewRoot != newC.RootHash {
			return fmt.Errorf("publish: consistency roots mismatch checkpoints %d→%d", old.TreeSize, newC.TreeSize)
		}
		cons = append(cons, proof)
		name := fmt.Sprintf("%d-%d.json", old.TreeSize, newC.TreeSize)
		if err := writeJSON(filepath.Join(out, "proofs", "consistency", name), proof); err != nil {
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

	// Per-server histories (hashes + metadata only).
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

	index := ServerIndex{Servers: make([]ServerIndexEntry, 0, len(ids))}
	for _, id := range ids {
		obs := byServer[id]
		file := serverFileName(id)
		index.Servers = append(index.Servers, ServerIndexEntry{
			ID:    id,
			File:  file,
			Count: len(obs),
		})
		// Incremental: rewrite server file if any observation idx >= Since.
		if opt.Since > 0 {
			touched := false
			for _, o := range obs {
				if o.Idx >= opt.Since {
					touched = true
					break
				}
			}
			if !touched {
				continue
			}
		}
		hist := ServerHistory{ID: id, Observations: obs}
		if err := writeJSON(filepath.Join(out, "servers", file), hist); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(out, "servers", "index.json"), index); err != nil {
		return err
	}

	// Inclusion proofs.
	from := opt.Since
	if from < 0 {
		from = 0
	}
	for i := int(from); i < len(leaves); i++ {
		path, err := log.InclusionAuditPath(i, leaves)
		if err != nil {
			return fmt.Errorf("publish: inclusion %d: %w", i, err)
		}
		pj := InclusionProofJSON{
			Index:     int64(i),
			TreeSize:  size,
			LeafHash:  hex.EncodeToString(leaves[i]),
			AuditPath: pathHex(path),
			RootHash:  rootHex,
		}
		if err := writeJSON(filepath.Join(out, "proofs", "inclusion", fmt.Sprintf("%d.json", i)), pj); err != nil {
			return err
		}
	}
	// Index for HTTP bundles that cannot directory-list.
	incIdx := make([]int64, 0, len(leaves)-int(from))
	for i := int(from); i < len(leaves); i++ {
		incIdx = append(incIdx, int64(i))
	}
	if from == 0 {
		if err := writeJSON(filepath.Join(out, "proofs", "inclusion", "index.json"), map[string]any{
			"count":   len(leaves),
			"indices": incIdx,
		}); err != nil {
			return err
		}
	}

	_ = latest
	return nil
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

func serverFileName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]) + ".json"
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
