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
	ctx, cancel := withTimeout(ctx, t.Timeout)
	defer cancel()

	if t.Command == "" && t.URL != "" {
		return observeHTTPAllowlisted(ctx, t)
	}
	if t.Command == "" {
		return observationFromClassified(t.ServerID, Classified{Event: EventRefused, Diagnostic: DiagNoTarget})
	}
	if t.SpeakMCP {
		return u.observeMCP(ctx, t)
	}
	return u.observeHelperJSON(ctx, t)
}

func (u *UnshareSandbox) observeMCP(ctx context.Context, t Target) Observation {
	if t.WorkDir == "" {
		return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
	}
	helper, err := u.jailHelperPath()
	if err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
	}

	jailDir, err := os.MkdirTemp("", "plumbline-jail-*")
	if err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
	}
	defer os.RemoveAll(jailDir)

	root := filepath.Join(jailDir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
	}
	workDst := filepath.Join(root, "work")
	if err := copyTree(t.WorkDir, workDst); err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
	}

	runtime := t.Command
	jailRuntime := ""
	switch {
	case runtime == "node" || strings.HasSuffix(runtime, "/node"):
		jp, err := stageNodeIntoJail(root)
		if err != nil {
			return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
		}
		jailRuntime = jp
	case runtime == "python" || runtime == "python3" || strings.HasSuffix(runtime, "/python3") || strings.HasSuffix(runtime, "/python"):
		// /usr is bind-mounted into the jail by plumbline-jail.
		jailRuntime = "/usr/bin/python3"
	default:
		// Copy arbitrary helper binary (containment tests use this path via SpeakMCP=false).
		return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
	}

	args := rewriteArgsToJail(t.WorkDir, t.Args)
	env := rewriteEnvToJail(t.WorkDir, t.Env)

	unshareArgs := []string{
		"--user", "--map-root-user",
		"--mount", "--net",
		"--pid", "--fork", "--kill-child",
		helper, root, jailRuntime,
	}
	unshareArgs = append(unshareArgs, args...)

	return observeStdioMCP(ctx, t.ServerID, "unshare", unshareArgs, env, "")
}

func (u *UnshareSandbox) observeHelperJSON(ctx context.Context, t Target) Observation {
	helper, err := u.jailHelperPath()
	if err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
	}

	jailDir, err := os.MkdirTemp("", "plumbline-jail-*")
	if err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
	}
	defer os.RemoveAll(jailDir)

	root := filepath.Join(jailDir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
	}
	// Place target outside /tmp so the jail's tmpfs does not wipe it.
	target := filepath.Join(root, "observe")
	bin, err := os.ReadFile(t.Command)
	if err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
	}
	if err := os.WriteFile(target, bin, 0o755); err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
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
		return observationFromClassified(t.ServerID, Classified{Event: EventTimeout, Diagnostic: DiagTimeout})
	}
	if err != nil {
		if looksLikeObserveJSON(stdout.Bytes()) {
			return parseObserveOutput(t.ServerID, stdout.Bytes())
		}
		// Process failed to start or jail setup failed. Distinguish: if the
		// helper never ran, stderr from plumbline-jail setup → containment;
		// otherwise exec failure.
		if isJailSetupFailure(stderr.Bytes()) {
			return observationFromClassified(t.ServerID, Classified{Event: EventContainmentFailed, Diagnostic: DiagContainmentFailed})
		}
		return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
	}
	if looksLikeObserveJSON(stdout.Bytes()) {
		return parseObserveOutput(t.ServerID, stdout.Bytes())
	}
	return observationFromClassified(t.ServerID, Classified{Event: EventOK, Diagnostic: DiagOK})
}

func isJailSetupFailure(stderr []byte) bool {
	s := string(stderr)
	return strings.Contains(s, "jail setup:") || strings.Contains(s, "pivot_root") ||
		strings.Contains(s, "unshare:")
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
