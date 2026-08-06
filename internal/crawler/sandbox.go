package crawler

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Target describes what the sandbox should observe.
type Target struct {
	ServerID string
	// URL for streamable-HTTP MCP servers (preferred when present).
	URL string
	// Command/Args run a stdio binary inside the sandbox (tests / packages).
	Command string
	Args    []string
	Env     []string
	// WorkDir is the installed package tree. Required when SpeakMCP is set.
	// Copied or bind-mounted into the jail; never left network-reachable.
	WorkDir string
	// SpeakMCP means Command is an MCP stdio server (node/python). The sandbox
	// dials it via mcpclient after dropping network. When false, Command is a
	// helper that prints a single observation JSON object on stdout.
	SpeakMCP bool
	// AllowHost is the only network host the sandbox may contact (host:port or host).
	AllowHost string
	Timeout   time.Duration
}

// Sandbox runs one observation in isolation and destroys the environment after.
type Sandbox interface {
	Observe(ctx context.Context, t Target) Observation
}

// TargetFromRegistry builds a sandbox target.
// Packages population prefers the primary package (stdio). Remotes are used
// only when IncludeRemotes behaviour is applied by the caller for remote-only
// servers, or when no packages are present.
func TargetFromRegistry(s RegistryServer) Target {
	t := Target{
		ServerID: s.Name,
		Timeout:  60 * time.Second,
	}
	if pkg, ok := PrimaryPackage(s); ok {
		t.Args = []string{pkg.RegistryType, pkg.Identifier, pkg.Version}
		return t
	}
	for _, r := range s.Remotes {
		if r.URL == "" {
			continue
		}
		if r.Type == "streamable-http" || r.Type == "http" || r.Type == "sse" || r.Type == "" {
			t.URL = r.URL
			if u, err := url.Parse(r.URL); err == nil {
				t.AllowHost = u.Host
			}
			break
		}
	}
	return t
}

// DefaultSandbox picks Docker if the daemon responds, else UnshareJail on Linux.
// Set PLUMBLINE_SANDBOX=unshare to force the jail (needed on Docker Desktop + WSL
// where Linux bind mounts into containers are empty).
func DefaultSandbox() (Sandbox, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLUMBLINE_SANDBOX"))) {
	case "unshare", "jail":
		return NewUnshareSandbox()
	case "docker":
		return NewDockerSandbox("")
	}
	if sb, err := NewDockerSandbox(""); err == nil {
		return sb, nil
	}
	if sb, err := NewUnshareSandbox(); err == nil {
		return sb, nil
	}
	return nil, fmt.Errorf("crawler: no sandbox backend available (need Docker daemon or unshare)")
}
