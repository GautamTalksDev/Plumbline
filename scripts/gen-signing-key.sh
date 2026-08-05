#!/usr/bin/env bash
# Generate an Ed25519 signing keypair OUTSIDE the repository.
# Usage: ./scripts/gen-signing-key.sh [output-dir]
# Default output-dir: $HOME/.plumbline (not in the git tree).
set -euo pipefail

OUT="${1:-${HOME}/.plumbline}"
mkdir -p "$OUT"
chmod 700 "$OUT"

PRIV="$OUT/signing.ed25519"
PUB="$OUT/signing.ed25519.pub"

if [[ -e "$PRIV" || -e "$PUB" ]]; then
  echo "refusing to overwrite existing key in $OUT" >&2
  exit 1
fi

export PATH="${HOME}/.local/go/bin:${PATH}"
go run "$(dirname "$0")/gen_signing_key.go" "$PRIV" "$PUB"
chmod 600 "$PRIV"
chmod 644 "$PUB"
echo "wrote $PRIV"
echo "wrote $PUB"
echo "Keep the private key offline. Public key may be distributed with releases."
