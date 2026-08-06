package crawler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePyPIEntrypointIgnoresDependencyScripts(t *testing.T) {
	dir := t.TempDir()
	// Dependency dist-info with a console script that would win under glob-first.
	writeDistInfo(t, dir, "cffi-2.1.1", []string{
		"[console_scripts]",
		"cffi-gen-src = cffi._cffi_gen_src:run",
	})
	writeDistInfo(t, dir, "uvicorn-0.52.1", []string{
		"[console_scripts]",
		"uvicorn = uvicorn.main:main",
	})
	writeDistInfo(t, dir, "tablestakes-1.1.0", []string{
		"[console_scripts]",
		"tablestakes = tablestakes.__main__:main",
	})

	args, label, err := resolvePyPIEntrypoint(dir, "tablestakes")
	if err != nil {
		t.Fatal(err)
	}
	if label != "tablestakes" {
		t.Fatalf("label=%q want tablestakes", label)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "tablestakes.__main__") {
		t.Fatalf("args=%v should import tablestakes.__main__", args)
	}
	if strings.Contains(joined, "cffi") {
		t.Fatalf("picked dependency script: %v", args)
	}
}

func TestResolvePyPIEntrypointNormalizesUnderscore(t *testing.T) {
	dir := t.TempDir()
	writeDistInfo(t, dir, "intervals_icu_mcp-4.4.0", []string{
		"[console_scripts]",
		"intervals-icu-mcp = intervals_icu_mcp.server:main",
	})
	writeDistInfo(t, dir, "cffi-2.1.1", []string{
		"[console_scripts]",
		"cffi-gen-src = cffi._cffi_gen_src:run",
	})
	_, label, err := resolvePyPIEntrypoint(dir, "intervals-icu-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if label != "intervals-icu-mcp" {
		t.Fatalf("label=%q", label)
	}
}

func TestResolveNPMBinFromTargetOnly(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "node_modules", "@scope", "mcp")
	dep := filepath.Join(dir, "node_modules", "dep-with-bin")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dep, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(target, "package.json"), `{"name":"@scope/mcp","bin":{"mcp":"cli.js"}}`)
	mustWrite(t, filepath.Join(target, "cli.js"), "#!/usr/bin/env node\n")
	mustWrite(t, filepath.Join(dep, "package.json"), `{"name":"dep-with-bin","bin":{"dep":"x.js"}}`)
	mustWrite(t, filepath.Join(dep, "x.js"), "#!/usr/bin/env node\n")

	ep, err := resolveNPMBin(dir, "@scope/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(ep, filepath.Join("@scope", "mcp", "cli.js")) {
		t.Fatalf("ep=%s", ep)
	}
}

func TestClassifySpawnStderr(t *testing.T) {
	cases := []struct {
		stderr string
		want   string
	}{
		{"Error: SYNCPEN_API_KEY environment variable is not set", EventCredentialsRequired},
		{"SYMBIOTIC_API_TOKEN environment variable is required", EventCredentialsRequired},
		{"SyntaxError: Invalid regular expression flags", EventRuntimeIncompatible},
		{"ModuleNotFoundError: No module named 'mcp.server.fastmcp'", EventPackageBroken},
		{"getaddrinfo EAI_AGAIN github.com", EventNetworkRequired},
		{"Usage: skilldb [options] [command]", EventUsageMisconfigured},
		{"ReferenceError: File is not defined", EventRuntimeIncompatible},
		{"some obscure crash dump xyz", EventExecFailed},
	}
	for _, tc := range cases {
		ev, _ := classifySpawnStderr(tc.stderr)
		if ev != tc.want {
			t.Fatalf("stderr=%q got %s want %s", tc.stderr, ev, tc.want)
		}
	}
}

func TestEscapeStderrPreviewBounded(t *testing.T) {
	long := strings.Repeat("a", 3000) + "EXCEPTION_TAIL"
	got := escapeStderrPreview(long)
	if !strings.Contains(got, "EXCEPTION_TAIL") {
		t.Fatal("tail should be preserved")
	}
	if strings.Contains(got, "\x00") {
		t.Fatal("null byte not escaped")
	}
	// Escaped form of ≤2048 raw bytes should stay bounded.
	if len(got) > 5000 {
		t.Fatalf("preview too long: %d", len(got))
	}
}

func TestAppendPackageRuntimeArgs(t *testing.T) {
	base := []string{"/work/bin.js"}
	got := appendPackageRuntimeArgs(base, Package{
		Identifier:  "one-shot-ui",
		PackageArgs: []string{"mcp"},
	})
	if strings.Join(got, " ") != "/work/bin.js mcp" {
		t.Fatalf("got %v", got)
	}
	got = appendPackageRuntimeArgs([]string{"-c", "code"}, Package{
		Identifier:  "basic-memory",
		RuntimeArgs: []string{"basic-memory", "mcp"},
	})
	if strings.Join(got, " ") != "-c code mcp" {
		t.Fatalf("runtime drop: %v", got)
	}
}

func writeDistInfo(t *testing.T, root, name string, lines []string) {
	t.Helper()
	dir := filepath.Join(root, name+".dist-info")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "entry_points.txt"), strings.Join(lines, "\n")+"\n")
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
