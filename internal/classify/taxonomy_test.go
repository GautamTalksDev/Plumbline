package classify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/classify"
)

func taxonomyDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "taxonomy")
}

func TestAssess_taxonomyFixtures(t *testing.T) {
	root := taxonomyDir(t)
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
			wantB, err := os.ReadFile(filepath.Join(dir, "want.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want classify.Classification
			if err := json.Unmarshal(wantB, &want); err != nil {
				t.Fatal(err)
			}
			if want.Rules == nil {
				want.Rules = []string{}
			}
			got, err := classify.Assess(oldB, newB)
			if err != nil {
				t.Fatal(err)
			}
			if got.Rules == nil {
				got.Rules = []string{}
			}
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(want)
			if string(gj) != string(wj) {
				t.Fatalf("got %s\nwant %s", gj, wj)
			}
		})
	}
	if count < 45 {
		t.Fatalf("need ≥45 fixtures, found %d", count)
	}
}

func TestAssess_nearMissURLDoesNotFireR03(t *testing.T) {
	dir := filepath.Join(taxonomyDir(t), "R03_05_near_miss_existing_url")
	oldB, _ := os.ReadFile(filepath.Join(dir, "old.json"))
	newB, _ := os.ReadFile(filepath.Join(dir, "new.json"))
	got, err := classify.Assess(oldB, newB)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got.Rules {
		if r == "R03" {
			t.Fatalf("false positive R03: %#v", got)
		}
	}
}

func TestAssess_noModelAPI(t *testing.T) {
	// taxonomy-v1 is deterministic pure functions; this guards the contract.
	if classify.TaxonomyVersion != "taxonomy-v1" {
		t.Fatal(classify.TaxonomyVersion)
	}
}

func TestImperativesDataFileSynced(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Caller failed")
	}
	pkg := filepath.Join(filepath.Dir(file), "imperatives-v1.txt")
	repo := filepath.Join(filepath.Dir(file), "..", "..", "taxonomy", "imperatives-v1.txt")
	a, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("taxonomy/imperatives-v1.txt and internal/classify/imperatives-v1.txt diverge")
	}
}

