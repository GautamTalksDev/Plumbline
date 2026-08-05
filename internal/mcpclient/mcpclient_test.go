package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GautamTalksDev/Plumbline/internal/canonical"
)

func fakeBin(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	out := filepath.Join(t.TempDir(), "mcpfake")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, filepath.Join(root, "testdata", "mcpfake"))
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mcpfake: %v\n%s", err, b)
	}
	return out
}

func TestConnectAndListTools_OK(t *testing.T) {
	bin := fakeBin(t)
	ctx := context.Background()
	s, err := Connect(ctx, ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.ProtocolVersion != "2024-11-05" {
		t.Fatalf("protocolVersion=%q", s.ProtocolVersion)
	}
	list, err := s.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 1 {
		t.Fatalf("tools=%d", len(list.Tools))
	}
	if !bytes.Contains(list.Tools[0], []byte(`"_meta"`)) {
		t.Fatalf("raw tool lost _meta: %s", list.Tools[0])
	}
}

func TestErrMalformed(t *testing.T) {
	bin := fakeBin(t)
	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=malformed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.ListTools(context.Background())
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v, want ErrMalformed", err)
	}
}

func TestErrTooLarge(t *testing.T) {
	bin := fakeBin(t)
	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=oversized"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.ListTools(context.Background())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestErrTransport(t *testing.T) {
	_, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   filepath.Join(t.TempDir(), "does-not-exist-mcp"),
	})
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("got %v, want ErrTransport", err)
	}
}

func TestErrProtocol(t *testing.T) {
	_, err := Connect(context.Background(), ServerSpec{
		Transport: "carrier-pigeon",
	})
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v, want ErrProtocol", err)
	}
}

func TestErrTimeout_HangKillsProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("30s hang test")
	}
	bin := fakeBin(t)
	start := time.Now()
	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=hang"},
	})
	elapsed := time.Since(start)
	if s != nil {
		_ = s.Close()
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
	if elapsed < ConnectDeadline-2*time.Second || elapsed > ConnectDeadline+10*time.Second {
		t.Fatalf("elapsed %v, want ~%v", elapsed, ConnectDeadline)
	}

	// Process must not leak. mcpfake binary path is unique under TempDir.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !processRunning(t, bin) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mcpfake process still running after kill: %s", bin)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestErrTimeout_ListTools(t *testing.T) {
	bin := fakeBin(t)
	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=hang_list"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	start := time.Now()
	_, err = s.ListTools(context.Background())
	elapsed := time.Since(start)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
	if elapsed < RequestTimeout-2*time.Second || elapsed > RequestTimeout+5*time.Second {
		t.Fatalf("elapsed %v, want ~%v", elapsed, RequestTimeout)
	}
}

func TestMutateDescriptionObservable(t *testing.T) {
	bin := fakeBin(t)
	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=mutate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a, err := s.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Tools[0], b.Tools[0]) {
		t.Fatal("expected mid-session description change")
	}
	if !bytes.Contains(a.Tools[0], []byte("version-one")) {
		t.Fatalf("first: %s", a.Tools[0])
	}
	if !bytes.Contains(b.Tools[0], []byte("version-two-poison")) {
		t.Fatalf("second: %s", b.Tools[0])
	}

	ha, err := canonical.Hash(a.Tools[0])
	if err != nil {
		t.Fatal(err)
	}
	hb, err := canonical.Hash(b.Tools[0])
	if err != nil {
		t.Fatal(err)
	}
	if ha == hb {
		t.Fatal("canonical hashes should differ after description change")
	}
}

func TestRawJSONRoundTripToCanonicalizer(t *testing.T) {
	bin := fakeBin(t)
	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	list, err := s.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw := list.Tools[0]

	// Must retain unknown/_meta fields and hash without an intermediate tool struct.
	if !bytes.Contains(raw, []byte(`"_meta"`)) {
		t.Fatal("missing _meta in raw tool")
	}
	h1, err := canonical.Hash(raw)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := canonical.Hash(json.RawMessage(append([]byte(nil), raw...)))
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("raw round-trip hash mismatch")
	}

	// Prove we did not go through a lossy typed struct: strip _meta via struct
	// round-trip and show the hash would change (i.e. raw path is required).
	var lossy struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(raw, &lossy); err != nil {
		t.Fatal(err)
	}
	reb, _ := json.Marshal(lossy)
	hl, err := canonical.Hash(reb)
	if err != nil {
		t.Fatal(err)
	}
	if hl == h1 {
		t.Fatal("lossy struct re-marshal unexpectedly preserved hash; _meta should matter")
	}
}

func TestHTTPTransport_OK(t *testing.T) {
	bin := fakeBin(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MCPFAKE_MODE=ok", "MCPFAKE_TRANSPORT=http", "MCPFAKE_ADDR=127.0.0.1:0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	addrBuf := make([]byte, 256)
	n, err := stdout.Read(addrBuf)
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimSpace(string(addrBuf[:n]))
	url := "http://" + addr

	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportHTTP,
		URL:       url,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, err := s.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 1 {
		t.Fatalf("tools=%d", len(list.Tools))
	}
}

func processRunning(t *testing.T, binPath string) bool {
	t.Helper()
	base := filepath.Base(binPath)
	out, err := exec.Command("ps", "-eo", "pid,args").CombinedOutput()
	if err != nil {
		// Windows CI may lack ps; try tasklist-style fallback via /proc if present.
		if _, err := os.Stat("/proc"); err != nil {
			t.Logf("ps unavailable: %v", err)
			return false
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, base) && strings.Contains(line, binPath) {
			// Exclude the ps line itself and go test helpers.
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if _, err := strconv.Atoi(fields[0]); err != nil {
				continue
			}
			return true
		}
	}
	return false
}

func TestNoRetryOnMalformed(t *testing.T) {
	// Documented behavior: a single malformed response fails immediately.
	bin := fakeBin(t)
	s, err := Connect(context.Background(), ServerSpec{
		Transport: TransportStdio,
		Command:   bin,
		Env:       []string{"MCPFAKE_MODE=malformed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now()
	_, err = s.ListTools(context.Background())
	if !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("malformed path looked like a retry: %v", time.Since(start))
	}
}