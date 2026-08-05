//go:build linux

package crawler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// UnshareSandbox isolates command targets with Linux user/mount/net/pid namespaces
// and a pivot_root jail (via cmd/plumbline-jail). Network namespace has no egress.
// Host paths are unreachable after pivot_root. The jail directory is destroyed after.
type UnshareSandbox struct {
	JailHelper string // path to plumbline-jail; empty = look up
}

// NewUnshareSandbox checks that unshare is available.
func NewUnshareSandbox() (*UnshareSandbox, error) {
	if _, err := exec.LookPath("unshare"); err != nil {
		return nil, fmt.Errorf("crawler: unshare not found: %w", err)
	}
	return &UnshareSandbox{}, nil
}

// Observe runs Command inside a pivot_root jail, or allowlisted HTTP for remotes.
func (u *UnshareSandbox) Observe(ctx context.Context, t Target) Observation {
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if t.Command == "" && t.URL != "" {
		return observeHTTPAllowlisted(ctx, t)
	}
	if t.Command == "" {
		return Observation{ServerID: t.ServerID, EventType: EventRefused, Detail: "no command"}
	}

	helper, err := u.jailHelperPath()
	if err != nil {
		return Observation{ServerID: t.ServerID, EventType: EventSandbox, Detail: err.Error()}
	}

	jailDir, err := os.MkdirTemp("", "plumbline-jail-*")
	if err != nil {
		return Observation{ServerID: t.ServerID, EventType: EventSandbox, Detail: err.Error()}
	}
	defer os.RemoveAll(jailDir)

	root := filepath.Join(jailDir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Observation{ServerID: t.ServerID, EventType: EventSandbox, Detail: err.Error()}
	}
	// Place target outside /tmp so the jail's tmpfs does not wipe it.
	target := filepath.Join(root, "observe")
	bin, err := os.ReadFile(t.Command)
	if err != nil {
		return Observation{ServerID: t.ServerID, EventType: EventSandbox, Detail: err.Error()}
	}
	if err := os.WriteFile(target, bin, 0o755); err != nil {
		return Observation{ServerID: t.ServerID, EventType: EventSandbox, Detail: err.Error()}
	}

	args := []string{
		"--user", "--map-root-user",
		"--mount", "--net",
		"--pid", "--fork", "--kill-child",
		helper, root, "/observe",
	}
	args = append(args, t.Args...)

	cmd := exec.CommandContext(ctx, "unshare", args...)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp"}, t.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if ctx.Err() != nil {
		return Observation{ServerID: t.ServerID, EventType: EventTimeout, Detail: "wall clock exceeded"}
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		if looksLikeObserveJSON(stdout.Bytes()) {
			return parseObserveOutput(t.ServerID, stdout.Bytes())
		}
		return Observation{
			ServerID:  t.ServerID,
			EventType: EventSandbox,
			Detail:    "jailed exit: " + truncate([]byte(detail), 200),
		}
	}
	if looksLikeObserveJSON(stdout.Bytes()) {
		return parseObserveOutput(t.ServerID, stdout.Bytes())
	}
	return Observation{ServerID: t.ServerID, EventType: EventOK, Detail: "jailed"}
}

func (u *UnshareSandbox) jailHelperPath() (string, error) {
	if u.JailHelper != "" {
		return u.JailHelper, nil
	}
	if p := os.Getenv("PLUMBLINE_JAIL_HELPER"); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("plumbline-jail"); err == nil {
		return p, nil
	}
	candidates := []string{
		"plumbline-jail",
		filepath.Join("bin", "plumbline-jail"),
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "plumbline-jail"),
			filepath.Join(dir, "cmd", "plumbline-jail", "plumbline-jail"),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, err := filepath.Abs(c)
			if err != nil {
				return c, nil
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("crawler: plumbline-jail helper not found (go install ./cmd/plumbline-jail or set PLUMBLINE_JAIL_HELPER)")
}

func looksLikeObserveJSON(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '{'
}
