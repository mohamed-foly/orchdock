// Package privacy checks that nothing personal or machine-specific is
// committed: no absolute user paths, no e-mail addresses, and none of the
// words in the local, git-ignored .privacy-words file (one per line; it
// holds the names, projects and folders that must never appear in the
// repository, so it is never committed itself).
//
// It runs with `go test ./...` and in CI, where only the generic patterns
// apply because the word list exists only on the maintainer's machine.
package privacy

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var patterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a Windows user folder", regexp.MustCompile(`(?i)\b[a-z]:[\\/]+users[\\/]+[a-z0-9._-]+`)},
	{"a workspace folder on a drive", regexp.MustCompile(`(?i)\b[a-z]:[\\/]+workspace\b`)},
	{"a Unix home folder", regexp.MustCompile(`(?:^|[\s"'(=:])/(?:home|Users)/([A-Za-z0-9._-]+)`)},
	{"an e-mail address", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)},
}

// Generic stand-ins that are fine in examples and tests.
var allowedHomes = map[string]bool{"user": true, "u": true, "me": true, "you": true, "runner": true, "example": true, "name": true}

var allowedEmails = regexp.MustCompile(`(?i)(noreply@|@example\.(com|org|net)$|@users\.noreply\.github\.com$)`)

// root is the repository root (two levels up from this package).
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// files lists what git would commit: tracked files plus untracked ones that
// are not ignored.
func files(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git is not available: %v", err)
	}
	var list []string
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f != "" {
			list = append(list, f)
		}
	}
	return list
}

func localWords(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, ".privacy-words"))
	if err != nil {
		return nil
	}
	var words []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		w := strings.ToLower(strings.TrimSpace(sc.Text()))
		if w != "" && !strings.HasPrefix(w, "#") {
			words = append(words, w)
		}
	}
	return words
}

func TestNothingPersonalIsCommitted(t *testing.T) {
	dir := root(t)
	words := localWords(dir)
	if len(words) == 0 {
		t.Log("no .privacy-words file: checking the generic patterns only")
	}
	for _, f := range files(t, dir) {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || bytes.IndexByte(b, 0) >= 0 { // gone, or binary
			continue
		}
		if f == "go.sum" {
			continue
		}
		for n, line := range strings.Split(string(b), "\n") {
			for _, p := range patterns {
				for _, m := range p.re.FindAllStringSubmatch(line, -1) {
					switch {
					case p.name == "a Unix home folder" && allowedHomes[m[1]]:
					case p.name == "an e-mail address" && allowedEmails.MatchString(m[0]):
					default:
						t.Errorf("%s:%d: %s: %q", f, n+1, p.name, m[0])
					}
				}
			}
			low := strings.ToLower(line)
			for _, w := range words {
				if strings.Contains(low, w) {
					t.Errorf("%s:%d: a private word from .privacy-words", f, n+1) // the word itself is not printed
				}
			}
		}
		for _, w := range words { // file names too
			if strings.Contains(strings.ToLower(f), w) {
				t.Errorf("%s: a private word in the file name", f)
			}
		}
	}
}
