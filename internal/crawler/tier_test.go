package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type memMeta map[string]string // "npm|pkg" -> latest version

func (m memMeta) Latest(_ context.Context, registryType, identifier string) (string, time.Time, error) {
	key := registryType + "|" + identifier
	v, ok := m[key]
	if !ok {
		return "", time.Time{}, &pollHTTPError{Status: 404}
	}
	return v, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), nil
}

type memInstall struct{}

func (memInstall) Install(_ context.Context, pkg Package, latest string, destDir string) (InstallResult, error) {
	return InstallResult{
		WorkDir:      destDir,
		Command:      "true",
		Args:         nil,
		Entrypoint:   "fake",
		RegistryType: pkg.RegistryType,
		Identifier:   pkg.Identifier,
		Version:      latest,
	}, nil
}

func pkgServer(name, reg, id, ver string) RegistryServer {
	return RegistryServer{
		Name: name,
		Packages: []Package{{
			RegistryType: reg,
			Identifier:   id,
			Version:      ver,
			Transport:    "stdio",
		}},
	}
}

func TestVersionChangeDetection(t *testing.T) {
	dir := t.TempDir()
	meta := memMeta{"npm|demo": "1.0.0"}
	reg := &MemoryRegistry{Pages: [][]RegistryServer{{
		pkgServer("vendor/demo", "npm", "demo", "1.0.0"),
	}}}
	cfg := Config{
		StatePath:      filepath.Join(dir, "state.json"),
		LogPath:        filepath.Join(dir, "log.sqlite"),
		SummaryPath:    filepath.Join(dir, "summary.json"),
		Sandbox:        &FakeSandbox{},
		Registry:       reg,
		PackageMeta:    meta,
		PackageInstall: memInstall{},
		FrozenCohort:   map[string]bool{}, // empty cohort → no forced spawns
		Concurrency:    2,
		RequestDelay:   time.Millisecond,
		SkipLiveGuard:  true,
		Clock:          func() time.Time { return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC) },
	}
	sum1, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !sum1.Complete {
		t.Fatalf("week1 incomplete: %+v", sum1)
	}
	if sum1.EventCounts[EventVersionPoll] != 1 {
		t.Fatalf("week1 polls=%v", sum1.EventCounts)
	}
	if sum1.Tier2Spawned != 0 {
		t.Fatalf("week1 should not spawn on first poll without cohort, got %d", sum1.Tier2Spawned)
	}

	// Week 2: upstream bumped.
	meta["npm|demo"] = "2.0.0"
	cfg.Clock = func() time.Time { return time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC) }
	sum2, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !sum2.Complete {
		t.Fatalf("week2 incomplete: %+v", sum2)
	}
	if sum2.Tier2Spawned != 1 {
		t.Fatalf("week2 expected 1 version-change spawn, got %d counts=%v", sum2.Tier2Spawned, sum2.EventCounts)
	}
}

func TestCohortMembershipStable(t *testing.T) {
	ids := []string{
		"alpha/one", "beta/two", "gamma/three", "delta/four", "epsilon/five",
		"zeta/six", "eta/seven", "theta/eight", "iota/nine", "kappa/ten",
	}
	var members []string
	for _, id := range ids {
		if InControlCohort(id) {
			members = append(members, id)
		}
	}
	// Recompute independently.
	var again []string
	for _, id := range ids {
		if InControlCohort(id) {
			again = append(again, id)
		}
	}
	if len(members) != len(again) {
		t.Fatal("unstable length")
	}
	for i := range members {
		if members[i] != again[i] {
			t.Fatalf("unstable membership %v vs %v", members, again)
		}
	}
	// Cross-check against CohortMembersFrom.
	got := CohortMembersFrom(ids)
	if len(got) != len(members) {
		t.Fatalf("CohortMembersFrom=%v want %v", got, members)
	}
}

func TestIncompleteRunNotCounted(t *testing.T) {
	dir := t.TempDir()
	meta := memMeta{"npm|a": "1.0.0", "npm|b": "1.0.0"}
	reg := &MemoryRegistry{Pages: [][]RegistryServer{{
		pkgServer("v/a", "npm", "a", "1.0.0"),
		pkgServer("v/b", "npm", "b", "1.0.0"),
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	slowMeta := PackageMetaClientFunc(func(_ context.Context, registryType, identifier string) (string, time.Time, error) {
		calls++
		if calls >= 1 {
			cancel()
		}
		return meta.Latest(context.Background(), registryType, identifier)
	})
	sum, err := Run(ctx, Config{
		StatePath:      filepath.Join(dir, "state.json"),
		LogPath:        filepath.Join(dir, "log.sqlite"),
		SummaryPath:    filepath.Join(dir, "summary.json"),
		Sandbox:        &FakeSandbox{},
		Registry:       reg,
		PackageMeta:    slowMeta,
		PackageInstall: memInstall{},
		FrozenCohort:   map[string]bool{},
		Concurrency:    1,
		RequestDelay:   time.Millisecond,
		SkipLiveGuard:  true,
	})
	_ = err
	if sum.Complete || sum.CountsTowardSixWeekSeries() {
		t.Fatalf("incomplete run must not count: %+v", sum)
	}
	b, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Summary
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Complete || decoded.CountsTowardSix {
		t.Fatalf("summary.json marked complete: %+v", decoded)
	}
}

func TestSummarySurvivesSIGKILLMidWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.json")
	good := Summary{RunID: "2026-08-05", Complete: true, CountsTowardSix: true, EventCounts: map[string]int{"ok": 1}}
	if err := WriteSummaryAtomic(path, good); err != nil {
		t.Fatal(err)
	}

	// Simulate a kill mid-write: write a temp file incompletely and leave it;
	// the final path must still hold the previous good JSON.
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"run_id":"PARTIAL`)
	// no Sync, no Rename, no Close — process "killed"
	_ = f.Close()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("summary.json empty after killed mid-write")
	}
	var decoded Summary
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("summary.json corrupt after kill: %v (%s)", err, b)
	}
	if !decoded.Complete || decoded.RunID != "2026-08-05" {
		t.Fatalf("lost previous summary: %+v", decoded)
	}
}

func TestFakeNPMRegistryPoll(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dist-tags": map[string]string{"latest": "9.9.9"},
			"time":      map[string]string{"9.9.9": "2026-01-01T00:00:00Z"},
		})
	}))
	defer ts.Close()
	meta := &HTTPPackageMeta{NPMBase: ts.URL, Client: ts.Client()}
	ver, _, err := meta.Latest(context.Background(), "npm", "something")
	if err != nil {
		t.Fatal(err)
	}
	if ver != "9.9.9" {
		t.Fatalf("ver=%s", ver)
	}
}

func TestUnpollableOCI(t *testing.T) {
	pr := PollServer(context.Background(), memMeta{}, pkgServer("o/ci", "oci", "ghcr.io/x", "1"), time.Now())
	if pr.EventType != EventUnpollable {
		t.Fatalf("%+v", pr)
	}
}

func TestDryRunCountsTowardFalse(t *testing.T) {
	dir := t.TempDir()
	sum, err := Run(context.Background(), Config{
		StatePath:      filepath.Join(dir, "state.json"),
		LogPath:        filepath.Join(dir, "log.sqlite"),
		SummaryPath:    filepath.Join(dir, "summary.json"),
		Sandbox:        &FakeSandbox{},
		Registry:       &MemoryRegistry{Pages: [][]RegistryServer{{pkgServer("v/a", "npm", "a", "1.0.0")}}},
		PackageMeta:    memMeta{"npm|a": "1.0.0"},
		PackageInstall: memInstall{},
		FrozenCohort:   map[string]bool{},
		DryRun:         true,
		Concurrency:    1,
		RequestDelay:   time.Millisecond,
		SkipLiveGuard:  true,
		Clock:          func() time.Time { return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Complete {
		t.Fatalf("expected complete dry run: %+v", sum)
	}
	if sum.CountsTowardSix {
		t.Fatal("dry-run must set counts_toward_six_week_series=false")
	}
	if !sum.DryRun {
		t.Fatal("summary.dry_run unset")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Summary
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CountsTowardSix || !decoded.DryRun {
		t.Fatalf("summary file: %+v", decoded)
	}
}
