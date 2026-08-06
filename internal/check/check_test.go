package check_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/baseline"
	"github.com/GautamTalksDev/Plumbline/internal/check"
	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
	"github.com/GautamTalksDev/Plumbline/internal/safetext"
)

type stubSource struct {
	tools []json.RawMessage
}

func (s *stubSource) ListTools(context.Context) (mcpclient.ToolList, error) {
	return mcpclient.ToolList{ProtocolVersion: "2024-11-05", Tools: s.tools}, nil
}
func (s *stubSource) Close() error { return nil }

func isolateConfig(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("APPDATA", filepath.Join(tmp, "AppData", "Roaming"))
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeConfig(t *testing.T, body string) {
	t.Helper()
	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfg, "Claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude_desktop_config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRun_baselineCreatedThenDrift(t *testing.T) {
	isolateConfig(t)
	writeClaudeConfig(t, `{
  "mcpServers": {
    "demo": { "command": "true", "args": [] }
  }
}`)

	v1 := json.RawMessage(`{"name":"echo","description":"version-one","inputSchema":{"type":"object"},"_meta":{"k":1}}`)
	v2 := json.RawMessage(`{"name":"echo","description":"version-two\u001b[31mX\u202E","inputSchema":{"type":"object"},"_meta":{"k":1}}`)

	var n int
	opt := check.Options{
		Connect: func(ctx context.Context, spec mcpclient.ServerSpec) (check.ToolSource, error) {
			n++
			if n == 1 {
				return &stubSource{tools: []json.RawMessage{v1}}, nil
			}
			return &stubSource{tools: []json.RawMessage{v2}}, nil
		},
	}

	res, err := check.Run(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if !res.BaselineCreated {
		t.Fatal("expected baseline created")
	}
	if res.ExitCode != check.ExitClean {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if _, err := os.Stat(res.BaselinePath); err != nil {
		t.Fatal(err)
	}

	res2, err := check.Run(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if res2.BaselineCreated {
		t.Fatal("baseline should already exist")
	}
	if res2.ExitCode != check.ExitDrift {
		t.Fatalf("exit=%d want drift", res2.ExitCode)
	}
	if res2.DriftCount != 1 {
		t.Fatalf("drift=%d", res2.DriftCount)
	}
	var drift check.ToolObservation
	for _, o := range res2.Observations {
		if o.Status == "drift" {
			drift = o
		}
	}
	if drift.Diff == "" || !strings.Contains(drift.Diff, "description") {
		t.Fatalf("diff=%q", drift.Diff)
	}

	// Safe printing of attacker-controlled description.
	safe := safetext.String(string(v2))
	if strings.Contains(safe, "\x1b") || strings.ContainsRune(safe, '\u202e') {
		t.Fatalf("unsafe output: %q", safe)
	}

	// Accept clears drift on next check with same stub returning v2.
	if err := check.Accept(context.Background(), "demo", "echo", opt); err != nil {
		t.Fatal(err)
	}
	res3, err := check.Run(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if res3.ExitCode != check.ExitClean {
		t.Fatalf("after accept exit=%d observations=%+v", res3.ExitCode, res3.Observations)
	}
}

func TestRun_noServersCleanMachine(t *testing.T) {
	isolateConfig(t)
	res, err := check.Run(context.Background(), check.Options{
		Connect: func(context.Context, mcpclient.ServerSpec) (check.ToolSource, error) {
			t.Fatal("should not connect")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BaselineCreated {
		t.Fatal("expected empty baseline created")
	}
	if res.ExitCode != check.ExitClean {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if res.ServersFound != 0 {
		t.Fatalf("servers=%d", res.ServersFound)
	}
}

func TestExitCodes(t *testing.T) {
	if check.ExitClean != 0 || check.ExitDrift != 1 || check.ExitError != 2 {
		t.Fatalf("exit codes: %d %d %d", check.ExitClean, check.ExitDrift, check.ExitError)
	}
}

func TestDiscoverPathsUseOSJoin(t *testing.T) {
	isolateConfig(t)
	p, err := baseline.Path()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && strings.Contains(p, "/") && !strings.Contains(p, `\`) {
		t.Fatalf("path uses forward slashes only on Windows, expected filepath separators: %s", p)
	}
	if !strings.Contains(p, "plumbline") || !strings.Contains(p, "baseline.json") {
		t.Fatalf("path=%s", p)
	}
}
