package baseline_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GautamTalksDev/Plumbline/internal/baseline"
)

func TestSaveLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("APPDATA", filepath.Join(tmp, "AppData", "Roaming"))

	f := &baseline.File{Servers: map[string]baseline.ServerRecord{}}
	f.Set("s", "t", baseline.ToolRecord{Hash: "sha256:abc", Tool: []byte(`{"name":"t"}`)})
	if err := baseline.Save(f); err != nil {
		t.Fatal(err)
	}
	p, err := baseline.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	got, ok, err := baseline.Load()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	rec, ok := got.Get("s", "t")
	if !ok || rec.Hash != "sha256:abc" {
		t.Fatalf("%+v", rec)
	}
}
