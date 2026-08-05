package siteguard_test

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestXSSSinksAvoidInnerHTML ensures site JS never assigns untrusted data via
// innerHTML (the classic XSS sink for tool descriptions).
func TestXSSSinksAvoidInnerHTML(t *testing.T) {
	root := findSite(t)
	var bad []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".html") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		// Allow comments mentioning innerHTML; forbid assignments.
		for _, line := range strings.Split(s, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "<!--") || strings.HasPrefix(trim, "/*") {
				continue
			}
			if strings.Contains(line, ".innerHTML") || strings.Contains(line, "innerHTML =") {
				bad = append(bad, path+": "+trim)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("site must not use innerHTML (use textContent):\n%s", strings.Join(bad, "\n"))
	}
}

func TestScriptTagInertWhenEscaped(t *testing.T) {
	const payload = `<script>alert(1)</script><img src=x onerror=alert(1)>`
	escaped := html.EscapeString(payload)
	if strings.Contains(escaped, "<script>") || strings.Contains(escaped, "<img") {
		t.Fatalf("escaped HTML still contains tags: %q", escaped)
	}
	// Site rendering rule: always textContent / EscapeString — never innerHTML.
	if !strings.Contains(escaped, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag, got %q", escaped)
	}
}

func TestScriptTagRendersViaTextContentPattern(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(findSite(t), "js", "ui.js"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "textContent") {
		t.Fatal("ui.js must use textContent for untrusted strings")
	}
	if !strings.Contains(s, "function text") {
		t.Fatal("expected text() helper")
	}
}

func findSite(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		cand := filepath.Join(dir, "site")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("site/ not found")
		}
		dir = parent
	}
}
