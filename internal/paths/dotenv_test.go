package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyDotEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	content := "\ufeff# comment\nORCHDOCK_HOME=\"/data/od\"\nexport ORCHDOCK_SHELL='pwsh'\nORCHDOCK_SET=from-file\nPATH=/evil\nbroken line\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ORCHDOCK_SET": "from-env"}
	ApplyDotEnv(p, func(k string) (string, bool) { v, ok := env[k]; return v, ok }, func(k, v string) error { env[k] = v; return nil })
	want := map[string]string{"ORCHDOCK_HOME": "/data/od", "ORCHDOCK_SHELL": "pwsh", "ORCHDOCK_SET": "from-env"}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if _, ok := env["PATH"]; ok {
		t.Error("only ORCHDOCK_ keys may be set")
	}
	ApplyDotEnv(filepath.Join(t.TempDir(), "missing"), nil, nil) // no file: nothing happens
}
