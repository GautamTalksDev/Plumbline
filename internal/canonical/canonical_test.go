package canonical

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "canonical", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func mustHash(t *testing.T, raw []byte) string {
	t.Helper()
	h, err := Hash(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(h, "sha256:") {
		t.Fatalf("hash missing sha256: prefix: %q", h)
	}
	if len(h) != len("sha256:")+64 {
		t.Fatalf("unexpected hash length: %q", h)
	}
	return h
}

func TestCanonicalize_versionPrefix(t *testing.T) {
	raw := testdata(t, "lf.json")
	out, err := Canonicalize(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte(VersionPrefix)) {
		t.Fatalf("missing version prefix: %q", out[:min(32, len(out))])
	}
}

func TestHash_tableDriven(t *testing.T) {
	tests := []struct {
		name string
		// If pairB is set, hashes of pairA and pairB must be equal (or unequal if wantEqual=false).
		pairA     string
		pairB     string
		wantEqual bool
		// single-file checks
		file string
		oversized bool
		note      string
	}{
		{
			name:      "CRLF_vs_LF_identical_hash",
			pairA:     "crlf.json",
			pairB:     "lf.json",
			wantEqual: true,
		},
		{
			name:      "NFC_vs_NFD_identical_hash",
			pairA:     "nfc.json",
			pairB:     "nfd.json",
			wantEqual: true,
		},
		{
			name:      "key_order_identical_hash",
			pairA:     "keys_sorted.json",
			pairB:     "keys_unsorted.json",
			wantEqual: true,
		},
		{
			name:      "number_1_0_vs_1_identical_per_JCS",
			pairA:     "number_1_0.json",
			pairB:     "number_1.json",
			wantEqual: true,
			note: "encoding/json decodes both 1 and 1.0 as float64(1); JCS Number::toString emits \"1\". Documented: identical hash.",
		},
		{
			name:      "zero_width_space_different_hash",
			pairA:     "zwsp.json",
			pairB:     "no_zwsp.json",
			wantEqual: false,
		},
		{
			name: "nested_5_levels",
			file: "nested_5.json",
		},
		{
			name: "description_empty",
			file: "desc_empty.json",
		},
		{
			name: "description_null",
			file: "desc_null.json",
		},
		{
			name: "description_missing",
			file: "desc_missing.json",
		},
		{
			name:      "two_MiB_description_errors",
			oversized: true,
		},
	}

	var descHashes []string

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch {
			case tt.oversized:
				// Build a tool whose raw JSON exceeds 1 MiB (2 MiB description payload).
				desc := strings.Repeat("a", 2<<20)
				raw, err := json.Marshal(map[string]any{
					"name":        "huge",
					"description": desc,
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) <= MaxToolBytes {
					t.Fatalf("test setup: expected >1MiB input, got %d", len(raw))
				}
				_, err = Hash(json.RawMessage(raw))
				if err == nil {
					t.Fatal("expected error for 2 MiB description, got nil")
				}
				if !strings.Contains(err.Error(), "1 MiB") {
					t.Fatalf("expected size error, got %v", err)
				}
				_, err = Canonicalize(json.RawMessage(raw))
				if err == nil {
					t.Fatal("Canonicalize: expected error for 2 MiB description")
				}

			case tt.pairA != "":
				ha := mustHash(t, testdata(t, tt.pairA))
				hb := mustHash(t, testdata(t, tt.pairB))
				equal := ha == hb
				if equal != tt.wantEqual {
					t.Fatalf("hash equal=%v wantEqual=%v\n  %s: %s\n  %s: %s\n  note: %s",
						equal, tt.wantEqual, tt.pairA, ha, tt.pairB, hb, tt.note)
				}
				if tt.note != "" {
					t.Log(tt.note)
				}

			case tt.file != "":
				h := mustHash(t, testdata(t, tt.file))
				canon, err := Canonicalize(json.RawMessage(testdata(t, tt.file)))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.HasPrefix(canon, []byte(VersionPrefix)) {
					t.Fatal("missing version prefix")
				}
				switch tt.file {
				case "desc_empty.json", "desc_null.json", "desc_missing.json":
					descHashes = append(descHashes, h)
				case "nested_5.json":
					// Spot-check that deep path survived into canonical bytes.
					if !bytes.Contains(canon, []byte(`"l5"`)) {
						t.Fatalf("nested key l5 missing from canonical form: %s", canon)
					}
				}

			default:
				t.Fatal("invalid test case")
			}
		})
	}

	// empty / null / missing description must not collapse into one hash.
	t.Run("description_empty_null_missing_are_distinct", func(t *testing.T) {
		if len(descHashes) != 3 {
			t.Fatalf("expected 3 description variant hashes, got %d", len(descHashes))
		}
		if descHashes[0] == descHashes[1] || descHashes[0] == descHashes[2] || descHashes[1] == descHashes[2] {
			t.Fatalf("description variants must differ: empty=%s null=%s missing=%s",
				descHashes[0], descHashes[1], descHashes[2])
		}
	})
}

func TestCanonicalize_rejectsInvalidJSON(t *testing.T) {
	_, err := Canonicalize(json.RawMessage(`{`))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCanonicalize_rejectsEmpty(t *testing.T) {
	_, err := Canonicalize(json.RawMessage{})
	if err == nil {
		t.Fatal("expected empty input error")
	}
}

func TestCanonicalize_preservesFullToolSurface(t *testing.T) {
	raw := testdata(t, "crlf.json")
	out, err := Canonicalize(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"name"`, `"title"`, `"description"`, `"inputSchema"`,
		`"outputSchema"`, `"annotations"`, `"_meta"`,
	} {
		if !bytes.Contains(out, []byte(key)) {
			t.Fatalf("canonical form missing %s: %s", key, out)
		}
	}
}

func TestHash_bareCRNormalized(t *testing.T) {
	a, _ := json.Marshal(map[string]any{"name": "x", "description": "a\rb"})
	b, _ := json.Marshal(map[string]any{"name": "x", "description": "a\nb"})
	ha, err := Hash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("bare CR should normalize to LF: %s vs %s", ha, hb)
	}
}
