package crawler

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNPMInstallResolvesBinAndSpawns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("unshare jail is Linux-only")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}
	root := findTestdata(t)
	mcpTgz := filepath.Join(root, "npm-install", "plumbline-fake-mcp-1.0.0.tgz")

	dest := t.TempDir()
	inst := &CLIPackageInstaller{}
	res, err := inst.Install(context.Background(), Package{
		RegistryType: RegistryNPM,
		Identifier:   mcpTgz,
		Version:      "1.0.0",
	}, "", dest)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Entrypoint == "" {
		t.Fatal("empty entrypoint")
	}
	// Bundled dependency must be present (nested or hoisted).
	depPaths := []string{
		filepath.Join(dest, "node_modules", "plumbline-fake-dep"),
		filepath.Join(dest, "node_modules", "plumbline-fake-mcp", "node_modules", "plumbline-fake-dep"),
	}
	found := false
	for _, p := range depPaths {
		if _, err := os.Stat(p); err == nil {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("dependency not installed")
	}

	sb, err := NewUnshareSandbox()
	if err != nil {
		t.Skip(err)
	}
	sb.JailHelper = buildJailHelperLocal(t)

	obs := sb.Observe(context.Background(), Target{
		ServerID: "test/fake-mcp",
		Command:  res.Command,
		Args:     res.Args,
		Env:      res.Env,
		WorkDir:  res.WorkDir,
		SpeakMCP: true,
		Timeout:  30 * time.Second,
	})
	if obs.EventType != EventOK {
		t.Fatalf("event=%s diag=%s", obs.EventType, obs.Diagnostic)
	}
	if len(obs.Tools) != 1 {
		t.Fatalf("tools=%d", len(obs.Tools))
	}
}

func TestNPMEntrypointUnresolved(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}
	root := findTestdata(t)
	nobin := filepath.Join(root, "npm-install", "nobin-pkg")
	dest := t.TempDir()
	inst := &CLIPackageInstaller{}
	_, err := inst.Install(context.Background(), Package{
		RegistryType: RegistryNPM,
		Identifier:   nobin,
	}, "", dest)
	if !errors.Is(err, ErrEntrypointUnresolved) {
		t.Fatalf("want ErrEntrypointUnresolved, got %v", err)
	}

	obs := observePackageInstallThenSpawn(context.Background(), &FakeSandbox{}, inst, RegistryServer{
		Name: "test/nobin",
		Packages: []Package{{
			RegistryType: RegistryNPM,
			Identifier:   nobin,
		}},
	}, "")
	if obs.EventType != EventEntrypointUnresolved {
		t.Fatalf("event=%s", obs.EventType)
	}
}

func TestNPMIgnoreScripts(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}
	root := findTestdata(t)
	pkg := filepath.Join(root, "npm-install", "postinstall-pkg")
	dest := t.TempDir()
	inst := &CLIPackageInstaller{}
	_, err := inst.Install(context.Background(), Package{
		RegistryType: RegistryNPM,
		Identifier:   pkg,
	}, "", dest)
	// May succeed (bin present) or entrypoint-ok; either way scripts must not run.
	if err != nil && !errors.Is(err, ErrEntrypointUnresolved) && !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("unexpected: %v", err)
	}
	for _, marker := range []string{
		filepath.Join(dest, "HACKED"),
		filepath.Join(dest, "HACKED_PRE"),
		filepath.Join(dest, "node_modules", "plumbline-postinstall", "HACKED"),
		filepath.Join(dest, "node_modules", "plumbline-postinstall", "HACKED_PRE"),
		filepath.Join(pkg, "HACKED"),
		filepath.Join(pkg, "HACKED_PRE"),
	} {
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("lifecycle script ran; found %s", marker)
		}
	}
}

func TestObservePackageNeverEmitsUnpollable(t *testing.T) {
	obs := observePackageInstallThenSpawn(context.Background(), &FakeSandbox{}, &CLIPackageInstaller{}, RegistryServer{
		Name: "test/oci",
		Packages: []Package{{
			RegistryType: RegistryOCI,
			Identifier:   "example/img:1",
		}},
	}, "1")
	if obs.EventType == EventUnpollable {
		t.Fatal("tier-2 must not emit unpollable")
	}
	if obs.EventType != EventRefused {
		t.Fatalf("event=%s", obs.EventType)
	}
}

func findTestdata(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		cand := filepath.Join(dir, "testdata")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("testdata not found")
	return ""
}

func buildJailHelperLocal(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "plumbline-jail")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/plumbline-jail")
	// module root
	root := filepath.Dir(findTestdata(t))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=go1.23.8")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build jail: %v\n%s", err, b)
	}
	return out
}
