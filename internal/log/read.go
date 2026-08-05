package log

import (
	"database/sql"
	"fmt"
)

// StoredEntry is one log row as returned to HTTP clients / verifiers.
type StoredEntry struct {
	Idx             int64
	ServerID        string
	ToolName        string
	Canonical       []byte
	Hash            string
	ObservedAt      int64
	ProtocolVersion string
	CanonVersion    string
	LeafHash        string // lowercase hex
}

// LatestCheckpoint returns the most recently stored signed checkpoint, if any.
func (l *Log) LatestCheckpoint() (Checkpoint, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var cp Checkpoint
	err := l.db.QueryRow(`
		SELECT tree_size, root_hash, timestamp, signature
		FROM checkpoints
		ORDER BY rowid DESC
		LIMIT 1`).Scan(&cp.TreeSize, &cp.RootHash, &cp.Timestamp, &cp.Signature)
	if err == sql.ErrNoRows {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("log: latest checkpoint: %w", err)
	}
	return cp, true, nil
}

// ListCheckpoints returns checkpoints ordered by tree_size ascending.
func (l *Log) ListCheckpoints(limit int) ([]Checkpoint, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	rows, err := l.db.Query(`
		SELECT tree_size, root_hash, timestamp, signature
		FROM checkpoints
		ORDER BY tree_size ASC, timestamp ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("log: list checkpoints: %w", err)
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var cp Checkpoint
		if err := rows.Scan(&cp.TreeSize, &cp.RootHash, &cp.Timestamp, &cp.Signature); err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}

// Entries returns entries with from <= idx < to.
func (l *Log) Entries(from, to int64) ([]StoredEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if from < 0 {
		from = 0
	}
	if to < from {
		return nil, fmt.Errorf("log: invalid range [%d,%d)", from, to)
	}
	rows, err := l.db.Query(`
		SELECT idx, server_id, tool_name, canonical, hash,
		       observed_at, protocol_version, canon_version, leaf_hash
		FROM entries
		WHERE idx >= ? AND idx < ?
		ORDER BY idx ASC`, from, to)
	if err != nil {
		return nil, fmt.Errorf("log: entries: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// EntriesByServer returns all entries for a server, oldest first.
func (l *Log) EntriesByServer(serverID string) ([]StoredEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rows, err := l.db.Query(`
		SELECT idx, server_id, tool_name, canonical, hash,
		       observed_at, protocol_version, canon_version, leaf_hash
		FROM entries
		WHERE server_id = ?
		ORDER BY idx ASC`, serverID)
	if err != nil {
		return nil, fmt.Errorf("log: server history: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// GetEntry returns the entry at idx.
func (l *Log) GetEntry(idx int64) (StoredEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var e StoredEntry
	err := l.db.QueryRow(`
		SELECT idx, server_id, tool_name, canonical, hash,
		       observed_at, protocol_version, canon_version, leaf_hash
		FROM entries WHERE idx = ?`, idx).Scan(
		&e.Idx, &e.ServerID, &e.ToolName, &e.Canonical, &e.Hash,
		&e.ObservedAt, &e.ProtocolVersion, &e.CanonVersion, &e.LeafHash,
	)
	if err != nil {
		return StoredEntry{}, fmt.Errorf("log: get entry %d: %w", idx, err)
	}
	return e, nil
}

func scanEntries(rows *sql.Rows) ([]StoredEntry, error) {
	var out []StoredEntry
	for rows.Next() {
		var e StoredEntry
		if err := rows.Scan(
			&e.Idx, &e.ServerID, &e.ToolName, &e.Canonical, &e.Hash,
			&e.ObservedAt, &e.ProtocolVersion, &e.CanonVersion, &e.LeafHash,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
