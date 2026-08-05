// Package canonical turns an MCP tool definition into exactly one
// deterministic byte sequence (and its hash).
//
// It performs no I/O: no network, no filesystem.
//
// Canonicalization order is fixed and intentional:
//
//  1. Size-check then parse — reject oversized input before any work.
//  2. Unicode NFC on every string — equate equivalent code-point sequences
//     before any other textual rewrite so NFD/NFC pairs cannot diverge later.
//  3. Line endings CRLF/CR → LF — a separate textual policy applied after
//     NFC so newline handling is not conflated with Unicode composition.
//  4. Preserve zero-width, bidi, and control characters — they are part of
//     the observed surface (and potential attack material), not noise to strip.
//  5. RFC 8785 JCS serialize — lexicographic keys by UTF-16 code unit,
//     no insignificant whitespace, ES/JCS number formatting.
//  6. Version prefix — so a future plumbline-canon-v2 can coexist without
//     silently colliding with v1 hashes.
//  7. SHA-256 over the prefixed bytes — Hash returns "sha256:<hex>".
package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ucarion/jcs"
	"golang.org/x/text/unicode/norm"
)

// MaxToolBytes is the maximum accepted raw tool JSON size (1 MiB).
const MaxToolBytes = 1 << 20

// VersionPrefix is prepended to every canonical byte sequence.
// Bump this when the canonicalization rules change.
const VersionPrefix = "plumbline-canon-v1\n"

// Canonicalize returns the version-prefixed RFC 8785 canonical form of tool.
//
// tool is the full tool object JSON (name, title, description, inputSchema,
// outputSchema, annotations, _meta, and any other fields). Nothing is omitted
// or filtered: every key present in the input is present in the output.
func Canonicalize(tool json.RawMessage) ([]byte, error) {
	if len(tool) > MaxToolBytes {
		return nil, fmt.Errorf("canonical: tool definition exceeds 1 MiB (%d bytes)", len(tool))
	}
	if len(tool) == 0 {
		return nil, fmt.Errorf("canonical: empty input")
	}

	var v any
	if err := json.Unmarshal(tool, &v); err != nil {
		return nil, fmt.Errorf("canonical: parse JSON: %w", err)
	}

	v = normalizeValue(v)

	jcsBytes, err := jcs.Append(nil, v)
	if err != nil {
		return nil, fmt.Errorf("canonical: JCS serialize: %w", err)
	}

	out := make([]byte, 0, len(VersionPrefix)+len(jcsBytes))
	out = append(out, VersionPrefix...)
	out = append(out, jcsBytes...)
	return out, nil
}

// Hash returns "sha256:<hex>" of Canonicalize(tool).
func Hash(tool json.RawMessage) (string, error) {
	canon, err := Canonicalize(tool)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// normalizeValue walks the JSON data model and applies string rules in order:
// NFC, then newline normalization. Non-string leaves are unchanged.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case string:
		return normalizeString(x)
	case map[string]any:
		for k, val := range x {
			x[k] = normalizeValue(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = normalizeValue(val)
		}
		return x
	default:
		// float64, bool, nil — left as encoding/json produced them.
		return v
	}
}

// normalizeString applies steps 2–4: NFC, then CRLF/CR→LF.
// Zero-width, bidi overrides, and control characters are intentionally kept.
func normalizeString(s string) string {
	s = norm.NFC.String(s)
	// CRLF first so we do not turn \r\n into \n\n.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}
