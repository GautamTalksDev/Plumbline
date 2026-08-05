package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RegistryServer is one server listing from the official registry.
type RegistryServer struct {
	Name        string
	Description string
	Version     string
	Remotes     []Remote
	Packages    []Package
	OptOutMeta  bool
}

// Remote is a network transport endpoint.
type Remote struct {
	Type string
	URL  string
}

// Package is a registry-published installable package (stdio spawn target).
type Package struct {
	RegistryType string // npm, pypi, oci, mcpb, …
	Identifier   string
	Version      string // registry-pinned version
	Transport    string
}

// RegistryClient lists servers with cursor pagination.
type RegistryClient interface {
	ListServers(ctx context.Context, cursor string, limit int) (servers []RegistryServer, nextCursor string, err error)
}

type httpRegistry struct {
	base   string
	client *http.Client
	ua     string
}

// NewHTTPRegistry returns a client for the official MCP registry API.
func NewHTTPRegistry(base, userAgent string) RegistryClient {
	return &httpRegistry{
		base: base,
		ua:   userAgent,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (r *httpRegistry) ListServers(ctx context.Context, cursor string, limit int) ([]RegistryServer, string, error) {
	u, err := url.Parse(r.base + "/v0.1/servers")
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	if limit <= 0 {
		limit = 50
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("version", "latest")
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", r.ua)
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("crawler: registry: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("crawler: registry HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}

	var parsed struct {
		Servers []struct {
			Server struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Version     string `json:"version"`
				Remotes     []struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				} `json:"remotes"`
				Packages []struct {
					RegistryType string `json:"registryType"`
					Identifier   string `json:"identifier"`
					Version      string `json:"version"`
					Transport    struct {
						Type string `json:"type"`
					} `json:"transport"`
				} `json:"packages"`
			} `json:"server"`
			Meta map[string]json.RawMessage `json:"_meta"`
		} `json:"servers"`
		Metadata struct {
			NextCursor string `json:"nextCursor"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", fmt.Errorf("crawler: registry decode: %w", err)
	}

	out := make([]RegistryServer, 0, len(parsed.Servers))
	for _, item := range parsed.Servers {
		s := RegistryServer{
			Name:        item.Server.Name,
			Description: item.Server.Description,
			Version:     item.Server.Version,
			OptOutMeta:  metaOptOut(item.Meta),
		}
		for _, rem := range item.Server.Remotes {
			s.Remotes = append(s.Remotes, Remote{Type: rem.Type, URL: rem.URL})
		}
		for _, pkg := range item.Server.Packages {
			s.Packages = append(s.Packages, Package{
				RegistryType: pkg.RegistryType,
				Identifier:   pkg.Identifier,
				Version:      pkg.Version,
				Transport:    pkg.Transport.Type,
			})
		}
		out = append(out, s)
	}
	return out, parsed.Metadata.NextCursor, nil
}

func metaOptOut(meta map[string]json.RawMessage) bool {
	if meta == nil {
		return false
	}
	for _, key := range []string{"plumbline.org/opt-out", "plumbline/opt-out"} {
		raw, ok := meta[key]
		if !ok {
			continue
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			continue
		}
		switch t := v.(type) {
		case bool:
			return t
		case map[string]any:
			if b, ok := t["optOut"].(bool); ok {
				return b
			}
		}
	}
	return false
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// MemoryRegistry is an in-memory RegistryClient for tests.
type MemoryRegistry struct {
	Pages [][]RegistryServer // each page
}

func (m *MemoryRegistry) ListServers(_ context.Context, cursor string, _ int) ([]RegistryServer, string, error) {
	idx := 0
	if cursor != "" {
		fmt.Sscanf(cursor, "%d", &idx)
	}
	if idx >= len(m.Pages) {
		return nil, "", nil
	}
	next := ""
	if idx+1 < len(m.Pages) {
		next = fmt.Sprintf("%d", idx+1)
	}
	return m.Pages[idx], next, nil
}
