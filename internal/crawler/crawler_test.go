package crawler_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/crawler"
	"github.com/GautamTalksDev/Plumbline/internal/log"
)

func TestDockerRunArgsContainmentPosture(t *testing.T) {
	args := crawler.DockerRunArgs(crawler.Target{ServerID: "demo/server"})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--rm",
		"--user", "65534:65534",
		"--read-only",
		"--memory", "512m",
		"--cpus", "0.5",
		"--cap-drop", "ALL",
		"--network", "none",
		"--security-opt", "no-new-privileges",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("DockerRunArgs missing %q in %v", want, args)
		}
	}
	for _, forbid := range []string{"-v", "--volume", "--mount"} {
		for _, a := range args {
			if a == forbid {
				t.Fatalf("DockerRunArgs must not mount host paths, found %q", forbid)
			}
		}
	}
}

func TestContainmentMaliciousServer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("containment jail is Linux-only")
	}
	sb, err := crawler.NewUnshareSandbox()
	if err != nil {
		t.Skip(err)
	}
	sb.JailHelper = buildJailHelper(t)

	bin := buildMalicious(t)
	dir := t.TempDir()
	canary := filepath.Join(dir, "canary")
	if err := os.WriteFile(canary, []byte("SAFE"), 0o644); err != nil {
		t.Fatal(err)
	}

	obs := sb.Observe(context.Background(), crawler.Target{
		ServerID: "test/malicious",
		Command:  bin,
		Env:      []string{"HOST_CANARY=" + canary, "EXTERNAL_HOST=1.1.1.1:443"},
		Timeout:  15 * time.Second,
	})
	if obs.EventType == crawler.EventSandbox && strings.Contains(obs.Detail, "wrote canary") {
		t.Fatalf("escape: wrote canary: %+v", obs)
	}
	if obs.EventType == crawler.EventSandbox && strings.Contains(obs.Detail, "network egress") {
		t.Fatalf("escape: network egress: %+v", obs)
	}

	got, err := os.ReadFile(canary)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "SAFE" {
		t.Fatalf("host canary mutated: %q", got)
	}

	time.Sleep(200 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", "sleep 300.plumbline-containment").CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("child process escaped jail: %s", out)
	}
	if obs.EventType != crawler.EventOK {
		t.Fatalf("expected contained ok observation, got %+v", obs)
	}
	if !strings.Contains(obs.Detail, "contained") && obs.Detail != "" && obs.Detail != "jailed" {
		// parseObserveOutput sets detail=contained from malicious JSON
		t.Logf("observation detail: %s", obs.Detail)
	}
	t.Logf("contained observation: type=%s detail=%s", obs.EventType, obs.Detail)
}

func buildMalicious(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "malicious")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/malicious")
	cmd.Dir = findModuleRoot(t)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build malicious: %v\n%s", err, b)
	}
	return out
}

func buildJailHelper(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "plumbline-jail")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/plumbline-jail")
	cmd.Dir = findModuleRoot(t)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build plumbline-jail: %v\n%s", err, b)
	}
	return out
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func TestCrawlResumeNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	logPath := filepath.Join(dir, "log.sqlite")
	optOut := filepath.Join(dir, "opt-out.txt")
	_ = os.WriteFile(optOut, []byte("# empty\n"), 0o644)

	all := []crawler.RegistryServer{
		{Name: "a/one"},
		{Name: "a/two"},
		{Name: "a/three"},
		{Name: "a/four"},
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	sb := &crawler.FakeSandbox{Hook: func(tg crawler.Target) crawler.Observation {
		return crawler.Observation{ServerID: tg.ServerID, EventType: crawler.EventRefused, Detail: "down"}
	}}

	sum1, err := crawler.Run(context.Background(), crawler.Config{
		StatePath:     statePath,
		LogPath:       logPath,
		OptOutPath:    optOut,
		Sandbox:       sb,
		Registry:      &crawler.MemoryRegistry{Pages: [][]crawler.RegistryServer{all[:2]}},
		SigningKey:    priv,
		Concurrency:   2,
		RequestDelay:  time.Millisecond,
		SkipLiveGuard: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum1.ServersDone != 2 {
		t.Fatalf("first run done=%d", sum1.ServersDone)
	}

	// Simulate crash after logging a/three but before MarkCompleted:
	// put a/three in InFlight, leave queue with a/four, keep cursor exhausted.
	st, err := crawler.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	st.InFlight = map[string]crawler.RegistryServer{
		"a/three": all[2],
	}
	st.Queue = []crawler.RegistryServer{all[3]}
	st.PageCursor = "" // no further pages
	if err := crawler.SaveState(statePath, st); err != nil {
		t.Fatal(err)
	}

	// Pretend a/three was already appended before crash (status in log, still InFlight).
	if _, err := crawler.Run(context.Background(), crawler.Config{
		StatePath:  filepath.Join(dir, "mid-state.json"),
		LogPath:    logPath,
		OptOutPath: optOut,
		Sandbox: &crawler.FakeSandbox{Hook: func(tg crawler.Target) crawler.Observation {
			return crawler.Observation{ServerID: "a/three", EventType: crawler.EventTimeout, Detail: "pre-crash"}
		}},
		Registry:      &crawler.MemoryRegistry{Pages: [][]crawler.RegistryServer{{{Name: "a/three"}}}},
		Concurrency:   1,
		RequestDelay:  time.Millisecond,
		SkipLiveGuard: true,
	}); err != nil {
		t.Fatal(err)
	}

	sum2, err := crawler.Run(context.Background(), crawler.Config{
		StatePath:     statePath,
		LogPath:       logPath,
		OptOutPath:    optOut,
		Sandbox:       sb,
		Registry:      &crawler.MemoryRegistry{Pages: nil}, // nothing more from registry
		SigningKey:    priv,
		Concurrency:   2,
		RequestDelay:  time.Millisecond,
		SkipLiveGuard: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sum2.Resumed {
		t.Fatal("expected resumed")
	}

	lg, err := log.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	for _, name := range []string{"a/one", "a/two", "a/three", "a/four"} {
		n, err := lg.CountTool(name, crawler.StatusToolName)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s: want exactly 1 status entry, got %d", name, n)
		}
	}

	st2, err := crawler.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a/one", "a/two", "a/three", "a/four"} {
		if !st2.Completed[name] {
			t.Fatalf("lost entry: %s not completed", name)
		}
	}
}

func TestFailureEventTypesReported(t *testing.T) {
	dir := t.TempDir()
	reg := &crawler.MemoryRegistry{Pages: [][]crawler.RegistryServer{{
		{Name: "e/timeout"},
		{Name: "e/refused"},
		{Name: "e/malformed"},
		{Name: "e/opt"},
	}}}
	_ = os.WriteFile(filepath.Join(dir, "opt-out.txt"), []byte("e/opt\n"), 0o644)

	sb := &crawler.FakeSandbox{Hook: func(tg crawler.Target) crawler.Observation {
		switch tg.ServerID {
		case "e/timeout":
			return crawler.Observation{ServerID: tg.ServerID, EventType: crawler.EventTimeout, Detail: "t"}
		case "e/refused":
			return crawler.Observation{ServerID: tg.ServerID, EventType: crawler.EventRefused, Detail: "r"}
		case "e/malformed":
			return crawler.Observation{ServerID: tg.ServerID, EventType: crawler.EventMalformed, Detail: "m"}
		default:
			return crawler.Observation{ServerID: tg.ServerID, EventType: crawler.EventOK}
		}
	}}

	sum, err := crawler.Run(context.Background(), crawler.Config{
		StatePath:     filepath.Join(dir, "state.json"),
		LogPath:       filepath.Join(dir, "log.sqlite"),
		OptOutPath:    filepath.Join(dir, "opt-out.txt"),
		Sandbox:       sb,
		Registry:      reg,
		Concurrency:   2,
		RequestDelay:  time.Millisecond,
		SkipLiveGuard: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{crawler.EventTimeout, crawler.EventRefused, crawler.EventMalformed, crawler.EventOptOut} {
		if sum.EventCounts[ev] != 1 {
			t.Fatalf("event %s count=%d want 1; full=%v", ev, sum.EventCounts[ev], sum.EventCounts)
		}
	}
	b, _ := json.Marshal(sum.EventCounts)
	t.Logf("event counts: %s", b)
}

func TestLiveGuard(t *testing.T) {
	t.Setenv("PLUMBLINE_CRAWL_I_UNDERSTAND", "")
	_, err := crawler.Run(context.Background(), crawler.Config{
		StatePath: filepath.Join(t.TempDir(), "s"),
		LogPath:   filepath.Join(t.TempDir(), "l"),
	})
	if err == nil || !strings.Contains(err.Error(), "PLUMBLINE_CRAWL_I_UNDERSTAND") {
		t.Fatalf("expected live guard error, got %v", err)
	}
}

func TestOptOutHonoured(t *testing.T) {
	dir := t.TempDir()
	opt := filepath.Join(dir, "opt-out.txt")
	_ = os.WriteFile(opt, []byte("vendor/secret\n"), 0o644)
	called := 0
	sb := &crawler.FakeSandbox{Hook: func(tg crawler.Target) crawler.Observation {
		called++
		return crawler.Observation{ServerID: tg.ServerID, EventType: crawler.EventOK}
	}}
	sum, err := crawler.Run(context.Background(), crawler.Config{
		StatePath:  filepath.Join(dir, "state.json"),
		LogPath:    filepath.Join(dir, "log.sqlite"),
		OptOutPath: opt,
		Sandbox:    sb,
		Registry: &crawler.MemoryRegistry{Pages: [][]crawler.RegistryServer{{
			{Name: "vendor/secret"},
			{Name: "vendor/ok"},
		}}},
		Concurrency:   1,
		RequestDelay:  time.Millisecond,
		SkipLiveGuard: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.EventCounts[crawler.EventOptOut] != 1 {
		t.Fatalf("opt-out count=%v", sum.EventCounts)
	}
	if called != 1 {
		t.Fatalf("sandbox called %d times, want 1 (only non-opted-out)", called)
	}
}
