//go:build linux

package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DockerSandbox observes targets inside ephemeral Docker/Podman containers.
type DockerSandbox struct {
	Engine string // docker or podman
}

// NewDockerSandbox probes the container engine. Returns error if daemon is down.
func NewDockerSandbox(engine string) (*DockerSandbox, error) {
	if engine == "" {
		engine = "docker"
		if _, err := exec.LookPath("docker"); err != nil {
			engine = "podman"
		}
	}
	if _, err := exec.LookPath(engine); err != nil {
		return nil, fmt.Errorf("crawler: %s not found: %w", engine, err)
	}
	cmd := exec.Command(engine, "info")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("crawler: %s unavailable: %v (%s)", engine, err, truncateBytes(out, 120))
	}
	return &DockerSandbox{Engine: engine}, nil
}

// DockerRunArgs returns the isolation flags used for every container.
// Exported for tests that assert containment posture without a daemon.
// Package SpeakMCP spawns may add a single readonly bind of the ephemeral
// install workdir; see DockerPackageRunArgs.
func DockerRunArgs(t Target) []string {
	return []string{
		"run", "--rm",
		"--name", "plumbline-crawl-" + sanitizeName(t.ServerID),
		"--user", "65534:65534",
		"--read-only",
		// exec allowed: we stage the observe binary on tmpfs (no host mounts).
		"--tmpfs", "/tmp:rw,nosuid,size=64m",
		"--memory", "512m",
		"--memory-swap", "512m",
		"--cpus", "0.5",
		"--pids-limit", "64",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--network", "none",
		"--stop-timeout", "2",
	}
}

// DockerPackageRunArgs extends DockerRunArgs with a readonly bind of the
// ephemeral install workdir at /work (destroyed with the host temp dir).
func DockerPackageRunArgs(t Target) []string {
	args := DockerRunArgs(t)
	if t.WorkDir != "" {
		abs, err := filepath.Abs(t.WorkDir)
		if err == nil {
			args = append(args, "-v", abs+":/work:ro")
		}
	}
	return args
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 40 {
		out = out[:40]
	}
	if out == "" {
		out = "srv"
	}
	return out
}

// Observe runs the target in a one-shot container and destroys it (--rm).
func (d *DockerSandbox) Observe(ctx context.Context, t Target) Observation {
	ctx, cancel := withTimeout(ctx, t.Timeout)
	defer cancel()

	if t.Command == "" && t.URL == "" {
		return observationFromClassified(t.ServerID, Classified{Event: EventRefused, Diagnostic: DiagNoTarget})
	}
	if t.URL != "" && t.Command == "" {
		return observeHTTPAllowlisted(ctx, t)
	}
	if t.SpeakMCP {
		return d.observeMCP(ctx, t)
	}
	return d.observeHelperJSON(ctx, t)
}

func (d *DockerSandbox) observeMCP(ctx context.Context, t Target) Observation {
	if t.WorkDir == "" {
		return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
	}
	args := DockerPackageRunArgs(t)
	image, runtime, runtimeArgs := dockerPackageCommand(t)
	args = append(args, "-w", "/work", image, runtime)
	args = append(args, runtimeArgs...)

	env := rewriteEnvToJail(t.WorkDir, t.Env)
	return observeStdioMCP(ctx, t.ServerID, d.Engine, args, env, "")
}

func dockerPackageCommand(t Target) (image, runtime string, args []string) {
	jailArgs := rewriteArgsToJail(t.WorkDir, t.Args)
	switch {
	case t.Command == "node" || strings.HasSuffix(t.Command, "/node"):
		return "node:20-alpine", "node", jailArgs
	case t.Command == "python" || t.Command == "python3" ||
		strings.HasSuffix(t.Command, "/python") || strings.HasSuffix(t.Command, "/python3"):
		return "python:3.12-alpine", "python3", jailArgs
	default:
		return "alpine:3.20", t.Command, jailArgs
	}
}

func (d *DockerSandbox) observeHelperJSON(ctx context.Context, t Target) Observation {
	args := DockerRunArgs(t)
	// Feed the binary via stdin into alpine tmpfs — no host path mounts.
	args = append(args,
		"alpine:3.20",
		"sh", "-c",
		"cat >/tmp/target && chmod +x /tmp/target && exec /tmp/target \"$@\"",
		"--",
	)
	args = append(args, t.Args...)

	cmd := exec.CommandContext(ctx, d.Engine, args...)
	bin, err := os.ReadFile(t.Command)
	if err != nil {
		return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
	}
	cmd.Stdin = bytes.NewReader(bin)
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
		return observationFromClassified(t.ServerID, Classified{Event: EventExecFailed, Diagnostic: DiagExecFailed})
	}
	return parseObserveOutput(t.ServerID, stdout.Bytes())
}

func filterArgs(args []string, key, val string) []string {
	out := make([]string, 0, len(args))
	skip := false
	for i := 0; i < len(args); i++ {
		if skip {
			skip = false
			continue
		}
		if args[i] == key && i+1 < len(args) && args[i+1] == val {
			skip = true
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func parseObserveOutput(serverID string, raw []byte) Observation {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return observationFromClassified(serverID, Classified{Event: EventMalformed, Diagnostic: DiagMalformed})
	}
	var payload struct {
		Event           string            `json:"event"`
		ProtocolVersion string            `json:"protocolVersion"`
		Tools           []json.RawMessage `json:"tools"`
		Detail          string            `json:"detail"`
		Diagnostic      string            `json:"diagnostic"`
		HTTPStatus      *int              `json:"http_status"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return observationFromClassified(serverID, Classified{Event: EventMalformed, Diagnostic: DiagMalformed})
	}
	ev := payload.Event
	if ev == "" {
		ev = EventOK
	}
	ev = normalizeLegacyEvent(ev)
	diag := payload.Diagnostic
	if diag == "" {
		diag = payload.Detail
	}
	if diag == "sandbox_error" || diag == "package_fetch_failed" {
		diag = fixedDiagnosticForEvent(ev)
	}
	// Only accept known fixed diagnostics from the sandbox helper; never keep free text.
	if !isFixedDiagnostic(diag) {
		diag = fixedDiagnosticForEvent(ev)
	}
	return Observation{
		ServerID:        serverID,
		EventType:       ev,
		ProtocolVersion: payload.ProtocolVersion,
		Tools:           payload.Tools,
		Detail:          diag,
		Diagnostic:      diag,
		HTTPStatus:      payload.HTTPStatus,
		EventsVersion:   EventsVersionV2,
	}
}

func truncateBytes(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Keep filterArgs referenced for HTTP network flag surgery in tests.
var _ = filterArgs
