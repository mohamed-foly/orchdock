//go:build windows

package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// runCmd runs argv through a real host and returns the logged output.
func runCmd(t *testing.T, argv []string, env ...string) string {
	t.Helper()
	r := startRun(t, "", "", func(s *domain.RunSpec) {
		s.Argv = argv
		s.Env = append(os.Environ(), env...)
	})
	if _, ok := r.waitExit(15 * time.Second); !ok {
		t.Fatal("cmd.exe did not finish")
	}
	b, _ := os.ReadFile(filepath.Join(r.dir, LogFile))
	return string(b)
}

// A shell line given to cmd.exe must reach it as typed: quotes, &, ^ and
// %VAR% keep their cmd.exe meaning.
func TestCmdLinesRunAsTyped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bat := filepath.Join(dir, "say.bat")
	if err := os.WriteFile(bat, []byte("@echo said:%~1\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ line, want, not string }{
		{`echo "a b"`, `"a b"`, `\"`},
		{`echo one & echo two`, "two", ""},
		{`echo ^<tag^>`, "<tag>", "^"},
		{`echo value=%OD_CMD_TEST%`, "value=x y", "%OD_CMD_TEST%"},
		{`"` + bat + `" "hello world"`, "said:hello world", ""},
	}
	for _, c := range cases {
		out := runCmd(t, []string{"cmd.exe", "/d", "/s", "/c", c.line}, "OD_CMD_TEST=x y")
		if !strings.Contains(out, c.want) || (c.not != "" && strings.Contains(out, c.not)) {
			t.Errorf("%s\nwant %q (and no %q), got %q", c.line, c.want, c.not, out)
		}
	}
}

// A batch shim (npm installs its commands as .cmd files) in a folder with a
// space, given arguments with spaces, runs like it would from a prompt.
func TestBatchShimWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bat := filepath.Join(dir, "tool.cmd")
	if err := os.WriteFile(bat, []byte("@echo got:%~1:%~2\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the form the command package builds for a shim (see wrapShim)
	out := runCmd(t, []string{"cmd.exe", "/d", "/s", "/c", `"` + bat + `" "first arg" second`})
	if !strings.Contains(out, "got:first arg:second") {
		t.Fatalf("got %q", out)
	}
}
