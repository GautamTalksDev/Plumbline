package safetext_test

import (
	"strings"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/safetext"
)

func TestString_stripsANSIAndBidi(t *testing.T) {
	in := "hello\x1b[31mRED\x1b[0m \u202Eolled\u202C world\u200b!"
	out := safetext.String(in)
	if strings.Contains(out, "\x1b") {
		t.Fatalf("ANSI escape survived: %q", out)
	}
	if strings.ContainsRune(out, '\u202E') || strings.ContainsRune(out, '\u202C') {
		t.Fatalf("bidi override survived: %q", out)
	}
	if strings.ContainsRune(out, '\u200b') {
		t.Fatalf("ZWSP survived: %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Fatalf("lost text: %q", out)
	}
	// Visible escapes present
	if !strings.Contains(out, "\\x1b") && !strings.Contains(out, "\\u202e") {
		t.Fatalf("expected visible escapes, got %q", out)
	}
}

func TestString_preservesPlainText(t *testing.T) {
	in := "Fetch the user's calendar\nline2"
	if got := safetext.String(in); got != in {
		t.Fatalf("got %q want %q", got, in)
	}
}
