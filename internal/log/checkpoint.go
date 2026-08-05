package log

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Checkpoint is a signed tree head for the Merkle log.
type Checkpoint struct {
	TreeSize  int64
	RootHash  string // lowercase hex of Merkle root
	Timestamp int64  // unix seconds
	Signature string // base64-encoded Ed25519 signature
}

// checkpointPayload is the exact byte sequence that is signed/verified.
// Changing this format requires a new checkpoint version tag.
func checkpointPayload(treeSize int64, rootHash string, timestamp int64) []byte {
	var b strings.Builder
	b.WriteString("plumbline-checkpoint-v1\n")
	b.WriteString("tree_size=")
	b.WriteString(strconv.FormatInt(treeSize, 10))
	b.WriteByte('\n')
	b.WriteString("root_hash=")
	b.WriteString(rootHash)
	b.WriteByte('\n')
	b.WriteString("timestamp=")
	b.WriteString(strconv.FormatInt(timestamp, 10))
	b.WriteByte('\n')
	return []byte(b.String())
}

// SignCheckpoint computes the current tree head and signs it with priv.
// The checkpoint is also persisted in the checkpoints table.
func (l *Log) SignCheckpoint(priv ed25519.PrivateKey) (Checkpoint, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(priv) != ed25519.PrivateKeySize {
		return Checkpoint{}, fmt.Errorf("log: invalid ed25519 private key size")
	}

	leaves, err := l.leafHashesLocked()
	if err != nil {
		return Checkpoint{}, err
	}
	root := hex.EncodeToString(MerkleRoot(leaves))
	ts := time.Now().Unix()
	size := int64(len(leaves))

	payload := checkpointPayload(size, root, ts)
	sig := ed25519.Sign(priv, payload)

	cp := Checkpoint{
		TreeSize:  size,
		RootHash:  root,
		Timestamp: ts,
		Signature: base64.StdEncoding.EncodeToString(sig),
	}

	_, err = l.db.Exec(
		`INSERT INTO checkpoints (tree_size, root_hash, timestamp, signature) VALUES (?, ?, ?, ?)`,
		cp.TreeSize, cp.RootHash, cp.Timestamp, cp.Signature,
	)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("log: store checkpoint: %w", err)
	}
	return cp, nil
}

// VerifyCheckpoint verifies an Ed25519 signature over a checkpoint payload.
// It is a pure function: no I/O, no trust in the log operator beyond the key.
func VerifyCheckpoint(pub ed25519.PublicKey, cp Checkpoint) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(cp.Signature)
	if err != nil {
		return false
	}
	payload := checkpointPayload(cp.TreeSize, cp.RootHash, cp.Timestamp)
	return ed25519.Verify(pub, payload, sig)
}
