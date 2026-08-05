package crawler

import (
	"context"
	"fmt"
	"net/url"
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
	// AllowHost is the only network host the sandbox may contact (host:port or host).
	AllowHost string
	Timeout   time.Duration
}

// Sandbox runs one observation in isolation and destroys the environment after.
type Sandbox interface {
	Observe(ctx context.Context, t Target) Observation
}

// TargetFromRegistry picks an HTTP remote when available.
func TargetFromRegistry(s RegistryServer) Target {
	t := Target{
		ServerID: s.Name,
		Timeout:  60 * time.Second,
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
func DefaultSandbox() (Sandbox, error) {
	if sb, err := NewDockerSandbox(""); err == nil {
		return sb, nil
	}
	if sb, err := NewUnshareSandbox(); err == nil {
		return sb, nil
	}
	return nil, fmt.Errorf("crawler: no sandbox backend available (need Docker daemon or unshare)")
}
