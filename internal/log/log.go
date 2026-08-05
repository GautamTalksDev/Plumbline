// Package log implements an RFC 6962-style Merkle append-only log
// backed by pure-Go SQLite (modernc.org/sqlite, no cgo).
//
// There is no Update or Delete API. SQLite triggers abort any
// UPDATE or DELETE against the entries table.
package log

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// CanonVersionV1 is the canonicalization version stored with entries
// produced under plumbline-canon-v1.
const CanonVersionV1 = "plumbline-canon-v1"

// Entry is one append-only observation committed to the log.
type Entry struct {
	ServerID        string
	ToolName        string
	Canonical       []byte // version-prefixed canonical bytes
	Hash            string // "sha256:<hex>" from package canonical
	ObservedAt      int64  // unix seconds; zero means now at Append
	ProtocolVersion string
	CanonVersion    string // e.g. CanonVersionV1
}

// Log is a Merkle append-only log.
type Log struct {
	db     *sql.DB
	mu     sync.Mutex
	next   int64 // next idx to assign
	insert *sql.Stmt
}

// Open opens (or creates) a SQLite-backed log at path.
func Open(path string) (*Log, error) {
	// WAL + NORMAL sync: durable enough for a local append-only log while
	// keeping cross-platform pure-Go SQLite usable at test scale.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("log: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	l := &Log{db: db}
	if err := l.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	var max sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(idx) FROM entries`).Scan(&max); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("log: read max idx: %w", err)
	}
	if max.Valid {
		l.next = max.Int64 + 1
	}

	l.insert, err = db.Prepare(`INSERT INTO entries (
		idx, server_id, tool_name, canonical, hash,
		observed_at, protocol_version, canon_version, leaf_hash
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("log: prepare insert: %w", err)
	}

	return l, nil
}

// Close closes the underlying database.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.insert != nil {
		_ = l.insert.Close()
	}
	return l.db.Close()
}

func (l *Log) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS entries (
  idx INTEGER PRIMARY KEY,
  server_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  canonical BLOB NOT NULL,
  hash TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  protocol_version TEXT NOT NULL,
  canon_version TEXT NOT NULL,
  leaf_hash TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS checkpoints (
  tree_size INTEGER NOT NULL,
  root_hash TEXT NOT NULL,
  timestamp INTEGER NOT NULL,
  signature TEXT NOT NULL
);

CREATE TRIGGER IF NOT EXISTS entries_no_update
BEFORE UPDATE ON entries
BEGIN
  SELECT RAISE(ABORT, 'entries are append-only: UPDATE forbidden');
END;

CREATE TRIGGER IF NOT EXISTS entries_no_delete
BEFORE DELETE ON entries
BEGIN
  SELECT RAISE(ABORT, 'entries are append-only: DELETE forbidden');
END;
`
	if _, err := l.db.Exec(schema); err != nil {
		return fmt.Errorf("log: migrate: %w", err)
	}
	return nil
}

// Append inserts entry at the next index and returns that index.
// There is no update or delete method in this package.
func (l *Log) Append(entry Entry) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(entry.Canonical) == 0 {
		return 0, fmt.Errorf("log: canonical bytes required")
	}
	if entry.Hash == "" {
		return 0, fmt.Errorf("log: hash required")
	}
	if entry.ServerID == "" || entry.ToolName == "" {
		return 0, fmt.Errorf("log: server_id and tool_name required")
	}
	if entry.CanonVersion == "" {
		entry.CanonVersion = CanonVersionV1
	}
	if entry.ObservedAt == 0 {
		entry.ObservedAt = time.Now().Unix()
	}

	leaf := LeafHash(entry.Canonical)
	leafHex := hex.EncodeToString(leaf)
	idx := l.next

	_, err := l.insert.Exec(
		idx,
		entry.ServerID,
		entry.ToolName,
		entry.Canonical,
		entry.Hash,
		entry.ObservedAt,
		entry.ProtocolVersion,
		entry.CanonVersion,
		leafHex,
	)
	if err != nil {
		return 0, fmt.Errorf("log: append: %w", err)
	}
	l.next++
	return idx, nil
}

// Size returns the number of entries (tree size).
func (l *Log) Size() (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.next, nil
}

// RootHash returns the RFC 6962 Merkle tree hash of the current log
// as lowercase hex.
func (l *Log) RootHash() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	leaves, err := l.leafHashesLocked()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(MerkleRoot(leaves)), nil
}

// leafHashesLocked returns leaf hashes in index order.
func (l *Log) leafHashesLocked() ([][]byte, error) {
	rows, err := l.db.Query(`SELECT leaf_hash FROM entries ORDER BY idx ASC`)
	if err != nil {
		return nil, fmt.Errorf("log: leaf hashes: %w", err)
	}
	defer rows.Close()

	out := make([][]byte, 0, l.next)
	for rows.Next() {
		var hexHash string
		if err := rows.Scan(&hexHash); err != nil {
			return nil, fmt.Errorf("log: scan leaf hash: %w", err)
		}
		b, err := hex.DecodeString(hexHash)
		if err != nil {
			return nil, fmt.Errorf("log: decode leaf hash: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// EntryCanonical returns the canonical bytes for idx.
func (l *Log) EntryCanonical(idx int64) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b []byte
	err := l.db.QueryRow(`SELECT canonical FROM entries WHERE idx = ?`, idx).Scan(&b)
	if err != nil {
		return nil, fmt.Errorf("log: entry %d: %w", idx, err)
	}
	return b, nil
}

// dbForTest exposes the DB so tests can attempt forbidden mutations.
func (l *Log) dbForTest() *sql.DB { return l.db }
