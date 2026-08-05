//go:build linux

package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
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
		return nil, fmt.Errorf("crawler: %s unavailable: %v (%s)", engine, err, truncate(out, 120))
	}
	return &DockerSandbox{Engine: engine}, nil
}

// DockerRunArgs returns the isolation flags used for every container.
// Exported for tests that assert containment posture without a daemon.
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
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if t.Command == "" && t.URL == "" {
		return observationFromClassified(t.ServerID, Classified{Event: EventRefused, Diagnostic: DiagNoTarget})
	}
	if t.URL != "" && t.Command == "" {
		return observeHTTPAllowlisted(ctx, t)
	}

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
		return observationFromClassified(t.ServerID, Classified{Event: EventSandbox, Diagnostic: DiagSandbox})
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
		return observationFromClassified(t.ServerID, Classified{Event: EventSandbox, Diagnostic: DiagSandbox})
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
	diag := payload.Diagnostic
	if diag == "" {
		diag = payload.Detail
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

// Keep filterArgs referenced for HTTP network flag surgery in tests.
var _ = filterArgs
