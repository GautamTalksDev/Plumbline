// Package discover finds locally configured MCP servers from known client configs.
// It performs filesystem reads only — no network.
package discover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

// Server is one configured MCP server plus where it was found.
type Server struct {
	Name   string
	Source string // config file path
	Spec   mcpclient.ServerSpec
}

// Find returns MCP servers from platform config locations.
// Duplicate names: first discovery wins (earlier paths take precedence).
func Find() ([]Server, []string, error) {
	paths, err := candidatePaths()
	if err != nil {
		return nil, nil, err
	}

	var (
		out     []Server
		scanned []string
		seen    = map[string]bool{}
	)
	for _, p := range paths {
		scanned = append(scanned, p)
		servers, err := parseFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			// Unreadable/malformed config: skip but keep going.
			continue
		}
		for _, s := range servers {
			if seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			s.Source = p
			out = append(out, s)
		}
	}
	return out, scanned, nil
}

func candidatePaths() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("discover: home: %w", err)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("discover: config dir: %w", err)
	}

	var paths []string

	// Claude Desktop (UserConfigDir/Claude/...)
	paths = append(paths, filepath.Join(cfg, "Claude", "claude_desktop_config.json"))

	// Cursor user config
	paths = append(paths, filepath.Join(home, ".cursor", "mcp.json"))

	// Project-local configs (cwd)
	cwd, err := os.Getwd()
	if err == nil {
		paths = append(paths,
			filepath.Join(cwd, ".mcp.json"),
			filepath.Join(cwd, ".cursor", "mcp.json"),
		)
	}

	// Linux also checks XDG-style Claude under ~/.config (already via UserConfigDir).
	// Windows %APPDATA% is UserConfigDir; %USERPROFILE%\.cursor covered above.
	_ = runtime.GOOS

	return uniquePaths(paths), nil
}

func uniquePaths(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

type fileConfig struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

type serverEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Type    string            `json:"type"` // cursor sometimes uses "sse" / "http" / "stdio"
	Headers map[string]string `json:"headers"`
}

func parseFile(path string) ([]Server, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304 -- MCP client config path from well-known local locations
	if err != nil {
		return nil, err
	}
	var fc fileConfig
	if err := json.Unmarshal(b, &fc); err != nil {
		return nil, err
	}
	if len(fc.MCPServers) == 0 {
		return nil, nil
	}

	var out []Server
	for name, raw := range fc.MCPServers {
		var e serverEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		spec, ok := toSpec(e)
		if !ok {
			continue
		}
		out = append(out, Server{Name: name, Spec: spec})
	}
	return out, nil
}

func toSpec(e serverEntry) (mcpclient.ServerSpec, bool) {
	if e.URL != "" || strings.EqualFold(e.Type, "http") || strings.EqualFold(e.Type, "sse") {
		if e.URL == "" {
			return mcpclient.ServerSpec{}, false
		}
		return mcpclient.ServerSpec{
			Transport: mcpclient.TransportHTTP,
			URL:       e.URL,
			Headers:   e.Headers,
		}, true
	}
	if e.Command == "" {
		return mcpclient.ServerSpec{}, false
	}
	var env []string
	for k, v := range e.Env {
		env = append(env, k+"="+v)
	}
	return mcpclient.ServerSpec{
		Transport: mcpclient.TransportStdio,
		Command:   e.Command,
		Args:      e.Args,
		Env:       env,
	}, true
}
