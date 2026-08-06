package crawler

import (
	"bufio"
	"bytes"
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

//go:embed spawn_matchers-v1.txt
var spawnMatchersV1 []byte

type spawnMatcher struct {
	Event string
	Sub   string // lowercased substring
}

var (
	spawnMatchersOnce sync.Once
	spawnMatchers     []spawnMatcher
)

func loadSpawnMatchers() []spawnMatcher {
	spawnMatchersOnce.Do(func() {
		spawnMatchers = parseSpawnMatchers(spawnMatchersV1)
	})
	return spawnMatchers
}

func parseSpawnMatchers(raw []byte) []spawnMatcher {
	var out []spawnMatcher
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		event, sub, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		event = strings.TrimSpace(event)
		sub = strings.ToLower(strings.TrimSpace(sub))
		if event == "" || sub == "" {
			continue
		}
		switch event {
		case EventCredentialsRequired, EventRuntimeIncompatible, EventPackageBroken,
			EventNetworkRequired, EventUsageMisconfigured:
			out = append(out, spawnMatcher{Event: event, Sub: sub})
		}
	}
	return out
}

// classifySpawnStderr maps captured stderr to a fixed event class.
// Conservative: unmatched → exec_failed.
func classifySpawnStderr(stderr string) (event, diagnostic string) {
	lower := strings.ToLower(stderr)
	for _, m := range loadSpawnMatchers() {
		if strings.Contains(lower, m.Sub) {
			return m.Event, m.Event
		}
	}
	return EventExecFailed, DiagExecFailed
}

// escapeStderrPreview returns a bounded escaped preview safe for JSON storage.
// Never use this string as a diagnostic. Caps at mcpclient.MaxStderrCapture.
func escapeStderrPreview(raw string) string {
	b := []byte(raw)
	const max = 2048
	if len(b) > max {
		b = b[len(b)-max:]
	}
	q := strconv.Quote(string(b))
	if len(q) >= 2 {
		return q[1 : len(q)-1]
	}
	return q
}
