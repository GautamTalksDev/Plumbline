// Package baseline persists local tool-hash baselines under the user config dir.
package baseline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	dirName  = "plumbline"
	fileName = "baseline.json"
	version  = 1
)

// ToolRecord is one tool's last-accepted observation.
type ToolRecord struct {
	Hash string          `json:"hash"`
	Tool json.RawMessage `json:"tool"` // raw tool JSON for structural diffs
}

// ServerRecord holds tools for one MCP server name.
type ServerRecord struct {
	Tools map[string]ToolRecord `json:"tools"`
}

// File is the on-disk baseline document.
type File struct {
	Version   int                     `json:"version"`
	CreatedAt string                  `json:"created_at"`
	UpdatedAt string                  `json:"updated_at"`
	Servers   map[string]ServerRecord `json:"servers"`
}

// Path returns os.UserConfigDir()/plumbline/baseline.json.
func Path() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("baseline: config dir: %w", err)
	}
	return filepath.Join(cfg, dirName, fileName), nil
}

// Dir returns the plumbline config directory.
func Dir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, dirName), nil
}

// Load reads the baseline. ok=false if the file does not exist.
func Load() (*File, bool, error) {
	p, err := Path()
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(p) //nolint:gosec // G304 -- local baseline path under the user's plumbline config dir
	if err != nil {
		if os.IsNotExist(err) {
			return newEmpty(), false, nil
		}
		return nil, false, fmt.Errorf("baseline: read: %w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, false, fmt.Errorf("baseline: parse: %w", err)
	}
	if f.Servers == nil {
		f.Servers = map[string]ServerRecord{}
	}
	for name, srv := range f.Servers {
		if srv.Tools == nil {
			srv.Tools = map[string]ToolRecord{}
			f.Servers[name] = srv
		}
	}
	return &f, true, nil
}

func newEmpty() *File {
	now := time.Now().UTC().Format(time.RFC3339)
	return &File{
		Version:   version,
		CreatedAt: now,
		UpdatedAt: now,
		Servers:   map[string]ServerRecord{},
	}
}

// Save writes the baseline atomically.
func Save(f *File) error {
	if f == nil {
		return fmt.Errorf("baseline: nil file")
	}
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("baseline: mkdir: %w", err)
	}
	f.Version = version
	f.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if f.CreatedAt == "" {
		f.CreatedAt = f.UpdatedAt
	}
	if f.Servers == nil {
		f.Servers = map[string]ServerRecord{}
	}

	p, err := Path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("baseline: marshal: %w", err)
	}
	b = append(b, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("baseline: write: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("baseline: rename: %w", err)
	}
	return nil
}

// Get returns a tool record if present.
func (f *File) Get(server, tool string) (ToolRecord, bool) {
	if f == nil {
		return ToolRecord{}, false
	}
	srv, ok := f.Servers[server]
	if !ok {
		return ToolRecord{}, false
	}
	rec, ok := srv.Tools[tool]
	return rec, ok
}

// Set updates one tool in the baseline (accept semantics).
func (f *File) Set(server, tool string, rec ToolRecord) {
	if f.Servers == nil {
		f.Servers = map[string]ServerRecord{}
	}
	srv, ok := f.Servers[server]
	if !ok {
		srv = ServerRecord{Tools: map[string]ToolRecord{}}
	}
	if srv.Tools == nil {
		srv.Tools = map[string]ToolRecord{}
	}
	srv.Tools[tool] = rec
	f.Servers[server] = srv
}

// ReplaceServer replaces all tools for a server (used when creating a baseline).
func (f *File) ReplaceServer(server string, tools map[string]ToolRecord) {
	if f.Servers == nil {
		f.Servers = map[string]ServerRecord{}
	}
	f.Servers[server] = ServerRecord{Tools: tools}
}
