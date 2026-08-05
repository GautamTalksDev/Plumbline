package discover_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/discover"
	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

func TestFind_cursorAndClaude(t *testing.T) {
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

	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(cfg, "Claude")
	_ = os.MkdirAll(claudeDir, 0o700)
	_ = os.WriteFile(filepath.Join(claudeDir, "claude_desktop_config.json"), []byte(`{
  "mcpServers": {
    "claude-srv": { "command": "npx", "args": ["-y", "x"] }
  }
}`), 0o600)

	cursorDir := filepath.Join(tmp, ".cursor")
	_ = os.MkdirAll(cursorDir, 0o700)
	_ = os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte(`{
  "mcpServers": {
    "cursor-srv": { "url": "http://127.0.0.1:9/mcp" }
  }
}`), 0o600)

	_ = os.WriteFile(filepath.Join(tmp, ".mcp.json"), []byte(`{
  "mcpServers": {
    "local": { "command": "node", "args": ["server.js"], "env": {"A":"B"} }
  }
}`), 0o600)

	servers, scanned, err := discover.Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(scanned) < 3 {
		t.Fatalf("scanned=%v", scanned)
	}
	byName := map[string]discover.Server{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if byName["claude-srv"].Spec.Transport != mcpclient.TransportStdio {
		t.Fatalf("%+v", byName["claude-srv"])
	}
	if byName["cursor-srv"].Spec.Transport != mcpclient.TransportHTTP {
		t.Fatalf("%+v", byName["cursor-srv"])
	}
	if byName["local"].Spec.Command != "node" {
		t.Fatalf("%+v", byName["local"])
	}
}
