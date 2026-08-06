package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/mcpclient"
)

// observeStdioMCP connects to a stdio MCP server and lists tools.
func observeStdioMCP(ctx context.Context, serverID, command string, args, env []string, dir string) Observation {
	sess, err := mcpclient.Connect(ctx, mcpclient.ServerSpec{
		Transport: mcpclient.TransportStdio,
		Command:   command,
		Args:      args,
		Env:       env,
		Dir:       dir,
	})
	if err != nil {
		return observationFromSpawnErr(serverID, err)
	}
	defer func() { _ = sess.Close() }()
	list, err := sess.ListTools(ctx)
	if err != nil {
		return observationFromSpawnErr(serverID, err)
	}
	pv := list.ProtocolVersion
	if pv == "" {
		pv = sess.ProtocolVersion
	}
	obs := observationFromClassified(serverID, Classified{Event: EventOK, Diagnostic: DiagOK})
	obs.ProtocolVersion = pv
	obs.Tools = list.Tools
	return obs
}

func observationFromSpawnErr(serverID string, err error) Observation {
	var pe *mcpclient.ProcessExitError
	if errors.As(err, &pe) {
		ev, diag := classifySpawnStderr(pe.Stderr)
		// Silent clean exit with no stderr: server returned success while
		// speaking no MCP — a demonstrated permanent unobservable mode.
		if ev == EventExecFailed && pe.ExitCode == 0 && strings.TrimSpace(pe.Stderr) == "" && pe.Signal == "" {
			ev, diag = EventSilentExit, DiagSilentExit
		}
		obs := observationFromClassified(serverID, Classified{Event: ev, Diagnostic: diag})
		obs.StderrPreview = escapeStderrPreview(pe.Stderr)
		obs.ExitCode = pe.ExitCode
		obs.ExitCodeSet = true
		obs.ExitSignal = pe.Signal
		return obs
	}
	return observationFromClassified(serverID, Classify(err))
}

func exitCodePtr(obs Observation) *int {
	if !obs.ExitCodeSet {
		return nil
	}
	c := obs.ExitCode
	return &c
}

// observationBlind checks the invariant that a terminal non-ok state must not
// lose both stderr and exit status unless the event is timeout or containment.
func isProcessTerminalEvent(ev string) bool {
	switch ev {
	case EventExecFailed, EventCredentialsRequired, EventNetworkRequired,
		EventRuntimeIncompatible, EventPackageBroken, EventUsageMisconfigured,
		EventSilentExit, EventContainmentFailed:
		return true
	default:
		return false
	}
}

func observationBlind(obs Observation) bool {
	switch obs.EventType {
	case EventOK, EventTimeout, EventContainmentFailed, EventSandbox:
		return false
	}
	if obs.StderrPreview != "" || obs.ExitCodeSet || obs.ExitSignal != "" {
		return false
	}
	return true
}

// copyTree recursively copies src directory into dst.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// resolveHostRuntime finds node/python3 on the host for staging into a jail.
func resolveHostRuntime(name string) (string, error) {
	switch name {
	case "python", "python3":
		if p, err := exec.LookPath("python3"); err == nil {
			return filepath.EvalSymlinks(p)
		}
		return exec.LookPath("python")
	case "node":
		p, err := exec.LookPath("node")
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(p)
	default:
		p, err := exec.LookPath(name)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(p)
	}
}

// rewriteArgsToJail maps host absolute paths under workDir to /work/... jail paths.
func rewriteArgsToJail(workDir string, args []string) []string {
	out := make([]string, len(args))
	absWork, _ := filepath.Abs(workDir)
	for i, a := range args {
		if filepath.IsAbs(a) {
			if rel, err := filepath.Rel(absWork, a); err == nil && !strings.HasPrefix(rel, "..") {
				out[i] = filepath.ToSlash(filepath.Join("/work", rel))
				continue
			}
		}
		out[i] = a
	}
	return out
}

// rewriteEnvToJail rewrites PYTHONPATH and similar to jail paths.
func rewriteEnvToJail(workDir string, env []string) []string {
	absWork, _ := filepath.Abs(workDir)
	out := make([]string, 0, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			out = append(out, e)
			continue
		}
		if v == absWork || v == workDir {
			out = append(out, k+"=/work")
			continue
		}
		out = append(out, e)
	}
	return out
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = 60 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

func stageNodeIntoJail(root string) (jailPath string, err error) {
	host, err := resolveHostRuntime("node")
	if err != nil {
		return "", fmt.Errorf("node not found on host: %w", err)
	}
	// plumbline-jail bind-mounts host /bin and /usr over the jail root, so
	// stage the runtime under /work which is our copied install tree only.
	dest := filepath.Join(root, "work", ".runtime", "node")
	if err := copyFile(host, dest, 0o755); err != nil {
		return "", err
	}
	return "/work/.runtime/node", nil
}
