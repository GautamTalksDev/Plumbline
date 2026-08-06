//go:build linux

package crawler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJailPropagatesStderrAndExit42(t *testing.T) {
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	sb, err := NewUnshareSandbox()
	if err != nil {
		t.Skip(err)
	}
	sb.JailHelper = buildJailHelperLocal(t)

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, ".keep"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	obs := sb.Observe(context.Background(), Target{
		ServerID: "test/jail-exit42",
		Command:  "python3",
		Args:     []string{"-c", "import sys; sys.stderr.write('PLUMB_JAIL_MARKER\\n'); sys.exit(42)"},
		WorkDir:  work,
		SpeakMCP: true,
		Timeout:  15 * time.Second,
	})
	if observationBlind(obs) {
		t.Fatalf("blind observation: event=%s stderr=%q exitSet=%v exit=%d",
			obs.EventType, obs.StderrPreview, obs.ExitCodeSet, obs.ExitCode)
	}
	if !obs.ExitCodeSet || obs.ExitCode != 42 {
		t.Fatalf("exit set=%v code=%d want 42", obs.ExitCodeSet, obs.ExitCode)
	}
	if !strings.Contains(obs.StderrPreview, "PLUMB_JAIL_MARKER") {
		t.Fatalf("stderr=%q missing marker", obs.StderrPreview)
	}
}
