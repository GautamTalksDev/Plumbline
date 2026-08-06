package mcpclient

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// MaxStderrCapture is the maximum number of stderr bytes retained for diagnosis.
// We keep the *tail* (last N bytes): Python and most runtimes put the exception
// last, and the first-200-byte window systematically hid the cause.
const MaxStderrCapture = 2048

// limitedStderr keeps the last MaxStderrCapture bytes of stderr.
type limitedStderr struct {
	buf bytes.Buffer
}

func (l *limitedStderr) Write(p []byte) (int, error) {
	_, _ = l.buf.Write(p)
	if l.buf.Len() > MaxStderrCapture {
		b := l.buf.Bytes()
		keep := append([]byte(nil), b[len(b)-MaxStderrCapture:]...)
		l.buf.Reset()
		_, _ = l.buf.Write(keep)
	}
	return len(p), nil
}

func (l *limitedStderr) String() string {
	return l.buf.String()
}

// ProcessExitError is returned when a stdio MCP process exits before or during
// the protocol exchange. Unwraps to ErrTransport.
type ProcessExitError struct {
	ExitCode int
	Signal   string // e.g. "SIGTERM"; empty if not killed by signal
	Stderr   string // last MaxStderrCapture bytes; may be empty
	Cause    error
}

func (e *ProcessExitError) Error() string {
	if e == nil {
		return "mcpclient: process exit"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%v (exit=%d signal=%q stderr=%q)", e.Cause, e.ExitCode, e.Signal, truncateErr(e.Stderr, 80))
	}
	return fmt.Sprintf("mcpclient: process exited exit=%d signal=%q", e.ExitCode, e.Signal)
}

func (e *ProcessExitError) Unwrap() error { return ErrTransport }

func processExitFromWait(waitErr error, stderr string) *ProcessExitError {
	pe := &ProcessExitError{Stderr: stderr, Cause: fmt.Errorf("%w: process exited: %v", ErrTransport, waitErr)}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) && ee != nil {
		pe.ExitCode = ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			pe.Signal = ws.Signal().String()
		}
	}
	return pe
}

func truncateErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
