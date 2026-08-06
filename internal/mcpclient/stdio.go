package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *limitedStderr
	mu      sync.Mutex
	done    chan struct{}
	waitErr error
}

func dialStdio(lifeCtx context.Context, spec ServerSpec) (transport, error) {
	cmd := exec.Command(spec.Command, spec.Args...) //nolint:gosec // G204 -- command comes from the registry and is executed only inside the sandbox
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	// Own process group so cancel/Close can kill the whole tree.
	cmd.SysProcAttr = stdioSysProcAttr()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdin pipe: %v", ErrTransport, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: stdout pipe: %v", ErrTransport, err)
	}
	stderrBuf := &limitedStderr{}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start: %v", ErrTransport, err)
	}

	t := &stdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 64*1024),
		stderr: stderrBuf,
		done:   make(chan struct{}),
	}
	go func() {
		t.waitErr = cmd.Wait()
		close(t.done)
	}()

	go func() {
		select {
		case <-lifeCtx.Done():
			t.killProcessGroup()
		case <-t.done:
		}
	}()

	return t, nil
}

func (t *stdioTransport) roundTrip(ctx context.Context, req json.RawMessage) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimeout, err)
	}

	// Notifications have no id — write and return empty success.
	if isNotification(req) {
		if _, err := t.stdin.Write(append(append([]byte(nil), req...), '\n')); err != nil {
			return nil, fmt.Errorf("%w: write: %v", ErrTransport, err)
		}
		return json.RawMessage(`{"jsonrpc":"2.0","id":null,"result":{}}`), nil
	}

	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		if _, err := t.stdin.Write(append(append([]byte(nil), req...), '\n')); err != nil {
			ch <- result{err: fmt.Errorf("%w: write: %v", ErrTransport, err)}
			return
		}
		raw, err := readJSONLine(t.stdout, MaxResponseBytes)
		ch <- result{raw: raw, err: err}
	}()

	select {
	case <-ctx.Done():
		t.killProcessGroup()
		return nil, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
	case <-t.done:
		return nil, t.processExitErr()
	case r := <-ch:
		if r.err != nil {
			// Stdout often EOFs before Wait returns. Prefer ProcessExitError so
			// exit status and stderr are never dropped together on this race.
			if err := t.waitProcessExit(200 * time.Millisecond); err != nil {
				return nil, err
			}
			return r.raw, r.err
		}
		return r.raw, r.err
	}
}

func (t *stdioTransport) processExitErr() error {
	stderr := ""
	if t.stderr != nil {
		stderr = t.stderr.String()
	}
	return processExitFromWait(t.waitErr, stderr)
}

// waitProcessExit waits briefly for the child to finish and, if it has,
// returns a ProcessExitError. Returns nil if the process is still running.
func (t *stdioTransport) waitProcessExit(grace time.Duration) error {
	select {
	case <-t.done:
		return t.processExitErr()
	case <-time.After(grace):
	}
	select {
	case <-t.done:
		return t.processExitErr()
	default:
		return nil
	}
}

func (t *stdioTransport) close() error {
	_ = t.stdin.Close()
	t.killProcessGroup()
	select {
	case <-t.done:
		return nil
	case <-time.After(2 * time.Second):
		t.killProcessGroup()
		<-t.done
		return nil
	}
}

func (t *stdioTransport) killProcessGroup() {
	if t.cmd == nil || t.cmd.Process == nil {
		return
	}
	killProcessGroup(t.cmd.Process.Pid)
}

func isNotification(req json.RawMessage) bool {
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(req, &probe); err != nil {
		return false
	}
	return len(probe.ID) == 0 || string(probe.ID) == "null"
}

// readJSONLine reads one newline-delimited JSON message with a hard size cap.
func readJSONLine(r *bufio.Reader, max int) (json.RawMessage, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			// Drop trailing newline when present.
			piece := chunk
			if piece[len(piece)-1] == '\n' {
				piece = piece[:len(piece)-1]
				if len(piece) > 0 && piece[len(piece)-1] == '\r' {
					piece = piece[:len(piece)-1]
				}
			}
			if len(buf)+len(piece) > max {
				return nil, fmt.Errorf("%w: exceeded %d bytes", ErrTooLarge, max)
			}
			buf = append(buf, piece...)
		}
		if err == nil {
			// Got newline-terminated line.
			break
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF {
			if len(buf) == 0 {
				return nil, fmt.Errorf("%w: EOF", ErrTransport)
			}
			break
		}
		return nil, fmt.Errorf("%w: read: %v", ErrTransport, err)
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("%w: empty line", ErrMalformed)
	}
	if !json.Valid(buf) {
		return nil, fmt.Errorf("%w: %q", ErrMalformed, truncateForErr(buf))
	}
	out := make([]byte, len(buf))
	copy(out, buf)
	return out, nil
}

func truncateForErr(b []byte) string {
	const n = 64
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// stdioSysProcAttr is provided per-OS.
// killProcessGroup is provided per-OS.
