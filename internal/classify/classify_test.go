package classify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/classify"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "classify")
}

func TestDiff_fixtures(t *testing.T) {
	root := fixtureDir(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		count++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, name)
			oldB, err := os.ReadFile(filepath.Join(dir, "old.json"))
			if err != nil {
				t.Fatal(err)
			}
			newB, err := os.ReadFile(filepath.Join(dir, "new.json"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := classify.Diff(oldB, newB)
			if err != nil {
				t.Fatal(err)
			}

			wantPath := filepath.Join(dir, "want.json")
			wantB, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(wantB) == "null\n" || string(wantB) == "null" {
				// Soft fixtures: assert invariants only.
				assertSoft(t, name, got)
				return
			}
			var want []classify.Change
			if err := json.Unmarshal(wantB, &want); err != nil {
				t.Fatal(err)
			}
			if len(want) == 0 {
				if len(got) != 0 {
					t.Fatalf("want empty changes, got %#v", got)
				}
				return
			}
			gotJ, _ := json.Marshal(got)
			wantJ, _ := json.Marshal(want)
			if string(gotJ) != string(wantJ) {
				t.Fatalf("changes mismatch\ngot:  %s\nwant: %s", gotJ, wantJ)
			}
		})
	}
	if count < 20 {
		t.Fatalf("expected >=20 fixtures, found %d", count)
	}
}

func assertSoft(t *testing.T, name string, got []classify.Change) {
	t.Helper()
	switch name {
	case "02_reworded_description":
		if len(got) == 0 {
			t.Fatal("expected description changes")
		}
		for _, c := range got {
			if c.Path != "/description" {
				t.Fatalf("unexpected path %s", c.Path)
			}
		}
	case "04_added_required_property":
		var sawProp, sawReq bool
		for _, c := range got {
			if c.Path == "/inputSchema/properties/mode" && c.Kind == classify.Added {
				sawProp = true
			}
			if c.Path == "/inputSchema/required/1" && c.Kind == classify.Added && c.New == "mode" {
				sawReq = true
			}
		}
		if !sawProp || !sawReq {
			t.Fatalf("missing required-property changes: %#v", got)
		}
	case "13_added_output_schema":
		if len(got) != 1 || got[0].Path != "/outputSchema" || got[0].Kind != classify.Added {
			t.Fatalf("%#v", got)
		}
	case "17_two_optional_properties":
		if len(got) != 2 {
			t.Fatalf("want 2 adds, got %#v", got)
		}
		if got[0].Path > got[1].Path {
			t.Fatal("not sorted by path")
		}
	default:
		t.Fatalf("unhandled soft fixture %s", name)
	}
}

func TestDiff_typoIsOneSmallChange(t *testing.T) {
	old := []byte(`{"name":"t","description":"hello world caller"}`)
	neu := []byte(`{"name":"t","description":"hello world callr"}`)
	got, err := classify.Diff(old, neu)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 change, got %#v", got)
	}
	c := got[0]
	if c.Path != "/description" || c.Kind != classify.Modified {
		t.Fatalf("%#v", c)
	}
	if c.Old != "caller" || c.New != "callr" {
		t.Fatalf("not a small word change: %#v", c)
	}
}

func TestDiff_reorderedKeysEmpty(t *testing.T) {
	a := []byte(`{"name":"echo","description":"x","inputSchema":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}}}}`)
	b := []byte(`{"inputSchema":{"properties":{"b":{"type":"number"},"a":{"type":"string"}},"type":"object"},"description":"x","name":"echo"}`)
	got, err := classify.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("reordered keys must produce zero changes, got %#v", got)
	}
}

func TestDiff_stableAcrossRuns(t *testing.T) {
	oldB, _ := os.ReadFile(filepath.Join(fixtureDir(t), "17_two_optional_properties", "old.json"))
	newB, _ := os.ReadFile(filepath.Join(fixtureDir(t), "17_two_optional_properties", "new.json"))
	var first string
	for i := 0; i < 50; i++ {
		got, err := classify.Diff(oldB, newB)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(got)
		if i == 0 {
			first = string(b)
			continue
		}
		if string(b) != first {
			t.Fatalf("unstable output on run %d\n%s\nvs\n%s", i, first, b)
		}
	}
}

func TestDiff_sortedByPath(t *testing.T) {
	old := []byte(`{"name":"t","description":"a","z":1}`)
	neu := []byte(`{"name":"t","description":"b","z":2}`)
	got, err := classify.Diff(old, neu)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("%#v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Path > got[i].Path {
			t.Fatalf("not sorted: %#v", got)
		}
	}
}
