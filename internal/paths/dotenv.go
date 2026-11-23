package paths

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnv reads a .env file next to the running program and sets the
// ORCHDOCK_* variables it lists, unless the environment already has them.
// It is how a machine-specific setting (such as ORCHDOCK_HOME) stays out of
// the repository and out of shell profiles. Other keys are ignored.
func LoadDotEnv() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	ApplyDotEnv(filepath.Join(filepath.Dir(exe), ".env"), os.LookupEnv, os.Setenv)
}

// ApplyDotEnv applies one .env file: KEY=VALUE lines, # comments, optional
// "export ", and values in single or double quotes.
func ApplyDotEnv(path string, lookup func(string) (string, bool), set func(string, string) error) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || !strings.HasPrefix(k, "ORCHDOCK_") {
			continue
		}
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, exists := lookup(k); exists {
			continue // the real environment wins
		}
		_ = set(k, v)
	}
}
