package siteguard_test

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/safetext"
)

// TestXSSSinksAvoidInnerHTML ensures site JS/HTML never assign untrusted data
// via innerHTML or insertAdjacentHTML.
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
		for _, line := range strings.Split(s, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "<!--") || strings.HasPrefix(trim, "/*") {
				continue
			}
			if strings.Contains(line, ".innerHTML") || strings.Contains(line, "innerHTML =") ||
				strings.Contains(line, "insertAdjacentHTML") {
				bad = append(bad, path+": "+trim)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("site must not use innerHTML/insertAdjacentHTML (use textContent):\n%s", strings.Join(bad, "\n"))
	}
}

func TestNoInlineScriptOrStyle(t *testing.T) {
	root := findSite(t)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		// Allow external <script src=...> only.
		for _, line := range strings.Split(s, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "<script>") || strings.Contains(trim, "<script type") {
				t.Errorf("%s: inline script forbidden: %s", path, trim)
			}
			if strings.Contains(trim, `style="`) {
				t.Errorf("%s: inline style attribute forbidden: %s", path, trim)
			}
		}
		if !strings.Contains(s, "Content-Security-Policy") {
			t.Errorf("%s: missing CSP meta tag", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestScriptTagInertWhenEscaped(t *testing.T) {
	const payload = `<script>alert(1)</script><img src=x onerror=alert(1)>`
	escaped := html.EscapeString(payload)
	if strings.Contains(escaped, "<script>") || strings.Contains(escaped, "<img") {
		t.Fatalf("escaped HTML still contains tags: %q", escaped)
	}
	if !strings.Contains(escaped, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag, got %q", escaped)
	}
}

func TestUIUsesTextContentAndSafeDisplay(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(findSite(t), "js", "ui.js"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "textContent") {
		t.Fatal("ui.js must use textContent for untrusted strings")
	}
	if !strings.Contains(s, "function safeDisplay") {
		t.Fatal("expected safeDisplay() helper")
	}
	if !strings.Contains(s, "function text") {
		t.Fatal("expected text() helper")
	}
	if !strings.Contains(s, "encodeServerId") {
		t.Fatal("expected encodeServerId for attribute/URL escaping")
	}
}

// TestAttackerControlledFieldsRenderInert checks that a server id and an
// observation field containing <script>, ANSI, bidi override, and zero-width
// characters are neutralized by the same rules the site applies before
// textContent assignment (safeDisplay mirrors safetext.String).
func TestAttackerControlledFieldsRenderInert(t *testing.T) {
	payloads := []struct {
		name string
		raw  string
	}{
		{"script", `<script>alert(1)</script>`},
		{"ansi", "normal\x1b[31mRED\x1b[0m"},
		{"bidi", "safe\u202Eevil"},
		{"zwc", "zero\u200Bwidth"},
	}

	uiJS, err := os.ReadFile(filepath.Join(findSite(t), "js", "ui.js"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(uiJS)

	for _, p := range payloads {
		t.Run(p.name, func(t *testing.T) {
			got := safetext.String(p.raw)
			// textContent would make HTML tags inert; we also require control
			// chars to be escaped so they cannot spoof UI.
			switch p.name {
			case "script":
				// With textContent, angle brackets are data — not a DOM parse.
				// Ensure our display path still includes the literal characters
				// as text (not stripped into emptiness) and site never uses
				// innerHTML on them.
				if got != p.raw && !strings.Contains(got, "script") {
					t.Fatalf("unexpected safetext output: %q", got)
				}
			case "ansi":
				if strings.Contains(got, "\x1b") {
					t.Fatalf("ANSI escape still present: %q", got)
				}
				if !strings.Contains(got, "\\x1b") {
					t.Fatalf("expected visible ANSI escape, got %q", got)
				}
			case "bidi":
				if strings.ContainsRune(got, '\u202E') {
					t.Fatalf("bidi override still present: %q", got)
				}
				if !strings.Contains(got, "\\u202e") && !strings.Contains(got, "\\u202E") {
					t.Fatalf("expected escaped bidi, got %q", got)
				}
			case "zwc":
				if strings.ContainsRune(got, '\u200B') {
					t.Fatalf("zero-width still present: %q", got)
				}
				if !strings.Contains(got, "\\u200b") && !strings.Contains(got, "\\u200B") {
					t.Fatalf("expected escaped zwc, got %q", got)
				}
			}
		})
	}

	// Site JS must implement the same classes of neutralization.
	if !strings.Contains(js, "0x1b") {
		t.Error("ui.js must handle ANSI ESC")
	}
	if !strings.Contains(js, "0x202e") && !strings.Contains(js, "0x202E") {
		t.Error("ui.js must handle bidi override")
	}
	if !strings.Contains(js, "0x200b") && !strings.Contains(js, "0x200B") {
		t.Error("ui.js must handle zero-width characters")
	}
}

func TestHeadersStrictCSP(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(findSite(t), "_headers"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"X-Content-Type-Options: nosniff",
		"Referrer-Policy: no-referrer",
		"frame-ancestors 'none'",
		"script-src 'self'",
		"style-src 'self'",
		"connect-src 'self'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("_headers missing %q", want)
		}
	}
	if strings.Contains(s, "unsafe-inline") {
		t.Error("_headers must not allow unsafe-inline")
	}
	if strings.Contains(s, "fonts.googleapis") || strings.Contains(s, "fonts.gstatic") {
		t.Error("_headers must not allow Google Fonts")
	}
}

func TestNoFindingsPages(t *testing.T) {
	root := findSite(t)
	for _, name := range []string{"changes.html", "js/changes.js"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			t.Errorf("findings page %s must not exist during study", name)
		}
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
