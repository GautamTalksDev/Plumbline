package jsondiff_test

import (
	"strings"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/jsondiff"
)

func TestDiff_descriptionChange(t *testing.T) {
	old := []byte(`{"name":"echo","description":"safe"}`)
	neu := []byte(`{"name":"echo","description":"pwned","_meta":{"x":1}}`)
	d := jsondiff.Diff(old, neu)
	if !strings.Contains(d, "description") {
		t.Fatalf("missing description change: %s", d)
	}
	if !strings.Contains(d, "+") && !strings.Contains(d, "~") {
		t.Fatalf("expected change markers: %s", d)
	}
}
