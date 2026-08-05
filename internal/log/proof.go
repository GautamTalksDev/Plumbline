package log

import (
	"encoding/hex"
	"fmt"
)

// InclusionProof is a Merkle inclusion proof for one leaf.
type InclusionProof struct {
	Index     int64
	TreeSize  int64
	LeafHash  []byte
	AuditPath [][]byte
	RootHash  []byte
}

// ConsistencyProof is a Merkle consistency proof between two tree sizes.
type ConsistencyProof struct {
	OldSize  int64
	NewSize  int64
	OldRoot  []byte
	NewRoot  []byte
	AuditPath [][]byte
}

// InclusionProof returns an RFC 6962 inclusion proof for leaf idx.
func (l *Log) InclusionProof(idx int64) (InclusionProof, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	leaves, err := l.leafHashesLocked()
	if err != nil {
		return InclusionProof{}, err
	}
	n := int64(len(leaves))
	if n == 0 {
		return InclusionProof{}, errEmptyTree
	}
	if idx < 0 || idx >= n {
		return InclusionProof{}, fmt.Errorf("%w: %d (size %d)", errBadIndex, idx, n)
	}

	path, err := InclusionAuditPath(int(idx), leaves)
	if err != nil {
		return InclusionProof{}, err
	}
	root := MerkleRoot(leaves)
	leaf := append([]byte(nil), leaves[idx]...)
	return InclusionProof{
		Index:     idx,
		TreeSize:  n,
		LeafHash:  leaf,
		AuditPath: path,
		RootHash:  root,
	}, nil
}

// ConsistencyProof returns an RFC 6962 consistency proof between oldSize and
// the current tree (newSize). oldSize must be <= current size.
func (l *Log) ConsistencyProof(oldSize, newSize int64) (ConsistencyProof, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	leaves, err := l.leafHashesLocked()
	if err != nil {
		return ConsistencyProof{}, err
	}
	n := int64(len(leaves))
	if newSize != n {
		return ConsistencyProof{}, fmt.Errorf("%w: newSize %d != current %d", errBadTreeSize, newSize, n)
	}
	if oldSize < 0 || oldSize > newSize {
		return ConsistencyProof{}, errBadTreeSize
	}

	path, err := ConsistencyAuditPath(int(oldSize), int(newSize), leaves)
	if err != nil {
		return ConsistencyProof{}, err
	}

	var oldRoot []byte
	if oldSize == 0 {
		oldRoot = EmptyRoot()
	} else {
		oldRoot = MerkleRoot(leaves[:oldSize])
	}
	newRoot := MerkleRoot(leaves)

	return ConsistencyProof{
		OldSize:   oldSize,
		NewSize:   newSize,
		OldRoot:   oldRoot,
		NewRoot:   newRoot,
		AuditPath: path,
	}, nil
}

// RootHashBytes is a convenience decode of RootHash hex.
func RootHashBytes(hexHash string) ([]byte, error) {
	return hex.DecodeString(hexHash)
}
