package main

import (
	"crypto/ed25519"
	"fmt"
	"os"
)

func loadSigningKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("signing key %s: %w (run scripts/gen-signing-key.sh)", path, err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing key %s: want %d bytes, got %d", path, ed25519.PrivateKeySize, len(b))
	}
	return ed25519.PrivateKey(b), nil
}
