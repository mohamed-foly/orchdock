package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pty "github.com/aymanbagabas/go-pty"

	"github.com/mohamed-foly/orchdock/internal/attach"
	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/testchild"
)

// The test binary stands in for orchdock: as a host ("host <dir>") and as the
// command a host runs. Tests set the role to "cli" for processes they start.
func TestMain(m *testing.M) {
	switch os.Getenv(testchild.EnvRole) {
	case "cli":
		os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
	case "child":
		testchild.Main(os.Getenv(testchild.EnvMode), os.Getenv(testchild.EnvArg))
		os.Exit(0)
	}
	_ = os.Setenv(testchild.EnvRole, "cli") // processes started from here act as the CLI
	code := m.Run()
	os.Exit(code)
}

// orch runs the CLI in this process.
func orch(t *testing.T, home string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = Run(append([]string{"--home", home}, args...), &o, &e)
	return code, o.String(), e.String()
}

func mustOrch(t *testing.T, home string, args ...string) string {
	t.Helper()
	code, out, errOut := orch(t, home, args...)
	if code != 0 {
		t.Fatalf("orchdock %v exited %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return out
}

// addTestItem adds an item that runs the test binary in the given mode.
func addTestItem(t *testing.T, home, kind, alias, mode, arg string, extra ...string) {
	t.Helper()
	args := []string{"add", kind, alias, "--alias", alias, "--argv", os.Args[0], "--cwd", t.TempDir(),
		"--env", testchild.EnvRole + "=child", "--env", testchild.EnvMode + "=" + mode, "--env", testchild.EnvArg + "=" + arg}
	mustOrch(t, home, append(args, extra...)...)
}

func stopAllAtEnd(t *testing.T, home string) {
	t.Helper()
	t.Cleanup(func() { _, _, _ = orch(t, home, "stop", "--all", "--force") })
}

func TestVersionHomeAndHelp(t *testing.T) {
	home := t.TempDir()
	if out := mustOrch(t, home, "home"); strings.TrimSpace(out) != home {
		t.Fatalf("home %q", out)
	}
	help := mustOrch(t, home, "help")
	for _, name := range []string{"ls", "add", "edit", "rm", "run", "stop", "restart", "attach", "send", "logs", "history", "cmd", "doctor", "version", "home"} {
		if !strings.Contains(help, "  "+name) {
			t.Errorf("help does not mention %q", name)
		}
	}
	if strings.Contains(help, "host <runDir>") || strings.Contains(help, "  host ") {
		t.Error("the internal host command must not be listed")
	}
	if code, _, _ := orch(t, home, "nonsense"); code != ExitUsage {
		t.Fatalf("unknown command exit %d", code)
	}
}

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, ExitOK},
		{fmt.Errorf("x: %w", domain.ErrNotFound), ExitNotFound},
		{fmt.Errorf("x: %w", host.ErrNoHost), ExitNotFound},
		{fmt.Errorf("x: %w", domain.ErrConflict), ExitConflict},
		{fmt.Errorf("x: %w", domain.ErrStartFailed), ExitStart},
		{fmt.Errorf("x: %w", domain.ErrInvalid), ExitUsage},
		{&ExitError{Code: 42}, 42},
		{fmt.Errorf("wrapped: %w", &ExitError{Code: 7}), 7},
		{io.ErrUnexpectedEOF, ExitUsage},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("exitCode(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestAddLsEditRm(t *testing.T) {
	home := t.TempDir()
	if out := mustOrch(t, home, "ls"); !strings.Contains(out, "no items") {
		t.Fatalf("empty ls: %q", out)
	}
	if out := strings.TrimSpace(mustOrch(t, home, "add", "task", "Update", "all", "repos", "--cmd", "git pull", "--tag", "daily,git", "--env", "A=1")); out != "update-all-repos" {
		t.Fatalf("alias %q", out)
	}
	// The same title again gets a unique alias.
	if out := strings.TrimSpace(mustOrch(t, home, "add", "task", "Update all repos", "--cmd", "x")); out != "update-all-repos-2" {
		t.Fatalf("second alias %q", out)
	}
	mustOrch(t, home, "add", "service", "Board", "--alias", "board", "--cmd", "serve --port {port}", "--tag", "web")
	mustOrch(t, home, "add", "link", "Docs", "--alias", "docs", "--target", "https://example.invalid/")

	var rows []itemRow
	if err := json.Unmarshal([]byte(mustOrch(t, home, "ls", "--json")), &rows); err != nil || len(rows) != 4 {
		t.Fatalf("ls --json: %v %+v", err, rows)
	}
	if out := mustOrch(t, home, "ls", "--tag", "git"); !strings.Contains(out, "update-all-repos") || strings.Contains(out, "board") {
		t.Fatalf("tag filter: %q", out)
	}
	if out := mustOrch(t, home, "ls", "--kind", "service"); !strings.Contains(out, "board") || strings.Contains(out, "docs") {
		t.Fatalf("kind filter: %q", out)
	}

	mustOrch(t, home, "edit", "board", "--title", "Backlog board", "--add-tag", "kanban", "--remove-tag", "web", "--env", "K=v", "--keep-open", "--cwd", "/work")
	cat, err := catalog.New(home, nil).Load()
	if err != nil {
		t.Fatal(err)
	}
	var board domain.Item
	for _, it := range cat.Items {
		if it.Alias == "board" {
			board = it
		}
	}
	if board.Title != "Backlog board" || strings.Join(board.Tags, ",") != "kanban" || board.Command.Env["K"] != "v" ||
		!board.Interactive.KeepOpenOnExit || board.Command.Cwd != "/work" {
		t.Fatalf("edited item %+v", board)
	}
	mustOrch(t, home, "edit", "board", "--unset-env", "K", "--argv", "tool,--flag")
	cat, _ = catalog.New(home, nil).Load()
	for _, it := range cat.Items {
		if it.Alias == "board" && (len(it.Command.Env) != 0 || it.Command.Shell != "none" || len(it.Command.Argv) != 2 || it.Command.Line != "") {
			t.Fatalf("argv edit %+v", it.Command)
		}
	}
	if code, _, _ := orch(t, home, "edit", "board", "--alias", "docs"); code != ExitConflict {
		t.Fatalf("a taken alias must exit %d, got %d", ExitConflict, code)
	}

	mustOrch(t, home, "rm", "docs")
	if code, _, _ := orch(t, home, "rm", "docs"); code != ExitNotFound {
		t.Fatalf("rm of a removed item exit %d", code)
	}
}

func TestAddRefusesBadInput(t *testing.T) {
	home := t.TempDir()
	for name, args := range map[string][]string{
		"unknown kind":      {"add", "gadget", "X", "--cmd", "x"},
		"agent kind":        {"add", "agent", "X"},
		"cmd and argv":      {"add", "task", "X", "--cmd", "x", "--argv", "y"},
		"task without cmd":  {"add", "task", "X"},
		"bad env":           {"add", "task", "X", "--cmd", "x", "--env", "NOEQUALS"},
		"bad alias":         {"add", "task", "X", "--cmd", "x", "--alias", "Bad Alias!"},
		"missing title":     {"add", "task"},
		"link without goal": {"add", "link", "L"},
	} {
		if code, _, errOut := orch(t, home, args...); code != ExitUsage {
			t.Errorf("%s: exit %d (%s)", name, code, errOut)
		}
	}
	if out := mustOrch(t, home, "ls"); !strings.Contains(out, "no items") {
		t.Fatalf("a refused add must not change the catalog: %q", out)
	}
}

func TestReferencesAmbiguousAndMissing(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "add", "shell", "One", "--alias", "one")
	cat, _ := catalog.New(home, nil).Load()
	id := cat.Items[0].ID
	if code, _, _ := orch(t, home, "cmd", id); code != 0 {
		t.Fatal("an id must resolve")
	}
	if code, _, _ := orch(t, home, "cmd", id[:6]); code != 0 {
		t.Fatal("an id prefix must resolve")
	}
	if code, _, _ := orch(t, home, "cmd", "ghost"); code != ExitNotFound {
		t.Fatalf("missing item exit %d", code)
	}
	if code, _, errOut := orch(t, home, "cmd", "i_"); code != ExitNotFound {
		t.Fatalf("a too short prefix exit %d (%s)", code, errOut)
	}
	// Two items sharing an id prefix are ambiguous.
	err := catalog.New(home, nil).Update(func(f *catalog.File) error {
		f.Items = append(f.Items, f.NewItem(domain.KindShell, "Two", "two", time.Now()))
		f.Items[1].ID = id[:6] + strings.Repeat("z", len(id)-6)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	code, _, errOut := orch(t, home, "cmd", id[:6])
	if code != ExitUsage || !strings.Contains(errOut, "one, two") {
		t.Fatalf("ambiguous reference: exit %d %q", code, errOut)
	}
}

func TestRunWaitPassesTheExitCodeAndPrintsOutput(t *testing.T) {
	home := t.TempDir()
	addTestItem(t, home, "task", "ok", "exit", "0")
	addTestItem(t, home, "task", "bad", "exit", "3")
	code, out, errOut := orch(t, home, "run", "ok", "--wait")
	if code != 0 || !strings.Contains(out, "bye") || strings.Contains(out, "\x1b") {
		t.Fatalf("ok: exit %d, plain output %q (%s)", code, out, errOut)
	}
	if code, out, _ = orch(t, home, "run", "bad", "--wait"); code != 3 || !strings.Contains(out, "bye") {
		t.Fatalf("bad: exit %d, output %q", code, out)
	}
	if code, out, _ = orch(t, home, "run", "ok", "--wait", "--raw"); code != 0 || !strings.Contains(out, "\x1b") {
		t.Fatalf("--raw must keep the terminal stream, got %q", out)
	}
	hist := mustOrch(t, home, "history", "bad")
	if !strings.Contains(hist, "failed") || !strings.Contains(hist, "3") {
		t.Fatalf("history %q", hist)
	}
	var lines []domain.HistoryLine
	if err := json.Unmarshal([]byte(mustOrch(t, home, "history", "ok", "--json")), &lines); err != nil || len(lines) != 2 || lines[0].Outcome != domain.OutcomeOK {
		t.Fatalf("history --json: %v %+v", err, lines)
	}
	if out := mustOrch(t, home, "ls", "--failed"); !strings.Contains(out, "bad") || strings.Contains(out, "ok ") {
		t.Fatalf("ls --failed: %q", out)
	}
}

func TestRunStopRestartAndConflicts(t *testing.T) {
	home := t.TempDir()
	stopAllAtEnd(t, home)
	addTestItem(t, home, "service", "svc", "sleep", "")
	if out := mustOrch(t, home, "run", "svc"); !strings.Contains(out, "started svc") {
		t.Fatalf("run: %q", out)
	}
	if code, _, _ := orch(t, home, "run", "svc"); code != ExitConflict {
		t.Fatalf("second run exit %d", code)
	}
	if out := mustOrch(t, home, "ls", "--running"); !strings.Contains(out, "svc") {
		t.Fatalf("ls --running: %q", out)
	}
	if out := mustOrch(t, home, "restart", "svc"); !strings.Contains(out, "restarted svc") {
		t.Fatalf("restart: %q", out)
	}
	if out := mustOrch(t, home, "stop", "svc", "--force"); !strings.Contains(out, "stopped svc") {
		t.Fatalf("stop: %q", out)
	}
	if code, _, _ := orch(t, home, "stop", "svc"); code != ExitNotFound {
		t.Fatalf("stopping an idle item exit %d", code)
	}
	if code, _, _ := orch(t, home, "run", "ghost"); code != ExitNotFound {
		t.Fatalf("run of a missing item exit %d", code)
	}
	if code, _, errOut := orch(t, home, "rm", "svc"); code != 0 {
		t.Fatalf("rm of a stopped item: %d %s", code, errOut)
	}
}

func TestRmRefusesARunningItemAndStopAllEndsEverything(t *testing.T) {
	home := t.TempDir()
	stopAllAtEnd(t, home)
	addTestItem(t, home, "service", "a", "sleep", "")
	addTestItem(t, home, "service", "b", "sleep", "")
	mustOrch(t, home, "run", "a")
	mustOrch(t, home, "run", "b")
	if code, _, _ := orch(t, home, "rm", "a"); code != ExitConflict {
		t.Fatalf("rm of a running item exit %d", code)
	}
	out := mustOrch(t, home, "stop", "--all", "--force")
	if !strings.Contains(out, "stopped a") || !strings.Contains(out, "stopped b") {
		t.Fatalf("stop --all: %q", out)
	}
	if out := mustOrch(t, home, "ls", "--running"); !strings.Contains(out, "no items") {
		t.Fatalf("nothing should run: %q", out)
	}
}

func TestSendAndLogs(t *testing.T) {
	home := t.TempDir()
	stopAllAtEnd(t, home)
	addTestItem(t, home, "shell", "echoer", "echo", "")
	mustOrch(t, home, "run", "echoer")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(mustOrch(t, home, "logs", "echoer"), "ready") && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	mustOrch(t, home, "send", "echoer", "hello there", "--enter")
	for !strings.Contains(mustOrch(t, home, "logs", "echoer"), "echo:hello there") {
		if time.Now().After(deadline) {
			t.Fatalf("sent text never echoed: %q", mustOrch(t, home, "logs", "echoer"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code, _, _ := orch(t, home, "send", "ghost", "x"); code != ExitNotFound {
		t.Fatalf("send to a missing item exit %d", code)
	}
	mustOrch(t, home, "stop", "echoer", "--force")
	if out := mustOrch(t, home, "logs", "echoer", "--tail", "1"); strings.Count(strings.TrimSpace(out), "\n") > 0 {
		t.Fatalf("--tail 1 gave %q", out)
	}
	// Logs of a finished run stay available; --run picks it explicitly.
	runs, _ := catalogRuns(t, home, "echoer")
	if out := mustOrch(t, home, "logs", "echoer", "--run", runs); !strings.Contains(out, "echo:hello there") {
		t.Fatalf("logs --run: %q", out)
	}
	if code, _, _ := orch(t, home, "logs", "echoer", "--run", "r_doesnotexist"); code != ExitNotFound {
		t.Fatalf("an unknown run exit %d", code)
	}
}

func catalogRuns(t *testing.T, home, alias string) (string, error) {
	t.Helper()
	var lines []domain.HistoryLine
	if err := json.Unmarshal([]byte(mustOrch(t, home, "history", alias, "--json")), &lines); err != nil || len(lines) == 0 {
		t.Fatalf("history: %v %v", err, lines)
	}
	return lines[len(lines)-1].RunID, nil
}

func TestCmdPrintsThePastableCommand(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "add", "task", "Pull", "--alias", "pull", "--cmd", "git pull", "--shell", "bash", "--cwd", "/work/my app", "--env", "MODE=fast")
	full := mustOrch(t, home, "cmd", "pull")
	if !strings.Contains(full, "cd '/work/my app'") || !strings.Contains(full, "export MODE='fast'") || !strings.Contains(full, "git pull") {
		// bash is not on every machine: the shell may be missing, which is a clear error instead.
		code, _, errOut := orch(t, home, "cmd", "pull")
		if code == 0 || !strings.Contains(errOut, "bash") {
			t.Fatalf("full form %q", full)
		}
		t.Skip("bash is not installed here")
	}
	if bare := strings.TrimSpace(mustOrch(t, home, "cmd", "pull", "--bare")); bare != "git pull" {
		t.Fatalf("bare %q", bare)
	}
}

func TestVariantsCanBeChosenOnRun(t *testing.T) {
	home := t.TempDir()
	addTestItem(t, home, "task", "var", "exit", "0")
	err := catalog.New(home, nil).Update(func(f *catalog.File) error {
		f.Items[0].Command.Variants = []domain.Variant{{ID: "loud", Env: map[string]string{"X": "1"}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustOrch(t, home, "run", "var", "--variant", "loud", "--wait")
	if hist := mustOrch(t, home, "history", "var"); !strings.Contains(hist, "loud") {
		t.Fatalf("history must show the variant: %q", hist)
	}
	if code, _, _ := orch(t, home, "run", "var", "--variant", "nope", "--wait"); code != ExitNotFound {
		t.Fatalf("an unknown variant exit %d", code)
	}
}

func TestDoctorReports(t *testing.T) {
	home := t.TempDir()
	out := mustOrch(t, home, "doctor")
	if !strings.Contains(out, "data folder") || !strings.Contains(out, "catalog: 0 items") || strings.Contains(out, "FAIL") {
		t.Fatalf("doctor:\n%s", out)
	}
}

func TestTwentyConcurrentEditsDoNotCorruptTheCatalog(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "add", "shell", "Target", "--alias", "target")
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "--home", home, "edit", "target", "--add-tag", fmt.Sprintf("t%02d", i)) //nolint:gosec // the test binary acts as the CLI
			cmd.Env = os.Environ()
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Sprintf("edit %d: %v: %s", i, err, out)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	cat, err := catalog.New(home, nil).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cat.Items[0].Tags); got != n {
		t.Fatalf("%d of %d concurrent edits survived: %v", got, n, cat.Items[0].Tags)
	}
	if err := cat.Validate(); err != nil {
		t.Fatalf("catalog invalid after concurrent edits: %v", err)
	}
}

// --- attach ---------------------------------------------------------------

func TestAttachSendsInputAndDetachesWithoutStopping(t *testing.T) {
	home := t.TempDir()
	stopAllAtEnd(t, home)
	addTestItem(t, home, "shell", "att", "echo", "")
	mustOrch(t, home, "run", "att")

	pr, pw := io.Pipe()
	old := Stdin
	Stdin = pr
	defer func() { Stdin = old }()
	go func() {
		time.Sleep(1500 * time.Millisecond) // let the replay and the redraw nudge settle
		_, _ = pw.Write([]byte("hi\r"))
		time.Sleep(1500 * time.Millisecond)
		_, _ = pw.Write([]byte{'x', attach.DefaultDetachKey, 'y'}) // 'y' after the detach key must never be sent
	}()
	code, out, errOut := orch(t, home, "attach", "att")
	if code != 0 {
		t.Fatalf("attach exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "ready") || !strings.Contains(out, "echo:hi") {
		t.Fatalf("the replay and the echo of the typed line must be shown: %q", out)
	}
	if !strings.Contains(out, "detached from att - still running") {
		t.Fatalf("detach message missing: %q", out)
	}
	if ls := mustOrch(t, home, "ls", "--running"); !strings.Contains(ls, "att") {
		t.Fatal("detaching must leave the run running")
	}
	// Only "x" was sent (the "y" after the detach key was not): pressing Enter
	// now makes the command echo exactly what it received.
	mustOrch(t, home, "send", "att", "", "--enter")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(mustOrch(t, home, "logs", "att"), "echo:x") {
		if time.Now().After(deadline) {
			t.Fatalf("the typed text never reached the command: %q", mustOrch(t, home, "logs", "att"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Contains(mustOrch(t, home, "logs", "att"), "echo:xy") {
		t.Fatal("input after the detach key was sent")
	}
}

func TestAttachToAnEndingRunReportsTheExit(t *testing.T) {
	home := t.TempDir()
	stopAllAtEnd(t, home)
	addTestItem(t, home, "task", "short", "exit", "5", "--keep-open")
	mustOrch(t, home, "run", "short")
	time.Sleep(1500 * time.Millisecond) // the command is done, the terminal is held open
	pr, pw := io.Pipe()
	old := Stdin
	Stdin = pr
	defer func() { Stdin = old }()
	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = pw.Write([]byte("\r")) // Enter closes a terminal that was kept open
	}()
	code, out, _ := orch(t, home, "attach", "short")
	if code != 0 || !strings.Contains(out, "short ended with code 5") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

func TestAttachToAnIdleItemIsNotFound(t *testing.T) {
	home := t.TempDir()
	addTestItem(t, home, "shell", "idle", "sleep", "")
	if code, _, _ := orch(t, home, "attach", "idle"); code != ExitNotFound {
		t.Fatalf("exit %d", code)
	}
}

func TestLastLines(t *testing.T) {
	data := []byte("one\ntwo\nthree\nfour\n")
	cases := map[int]string{1: "four\n", 2: "three\nfour\n", 4: "one\ntwo\nthree\nfour\n", 10: "one\ntwo\nthree\nfour\n"}
	for n, want := range cases {
		if got := string(lastLines(data, n)); got != want {
			t.Errorf("lastLines(%d) = %q, want %q", n, got, want)
		}
	}
	if got := string(lastLines([]byte("a\nb"), 1)); got != "b" {
		t.Errorf("no trailing newline: %q", got)
	}
}

func TestCopyNewFollowsAFileThatGrowsAndShrinks(t *testing.T) {
	path := t.TempDir() + "/log"
	var out bytes.Buffer
	off := copyNew(path, 0, &out) // missing file: nothing
	if off != 0 || out.Len() != 0 {
		t.Fatal("a missing file must be ignored")
	}
	_ = os.WriteFile(path, []byte("hello "), 0o600)
	off = copyNew(path, off, &out)
	_ = os.WriteFile(path, []byte("hello world"), 0o600)
	off = copyNew(path, off, &out)
	_ = os.WriteFile(path, []byte("new"), 0o600) // the host trimmed the log
	copyNew(path, off, &out)
	if out.String() != "hello worldnew" {
		t.Fatalf("got %q", out.String())
	}
}

// syncBuffer is a bytes.Buffer safe for use from several goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAttachInARealTerminal runs "orchdock attach" inside a pseudo-terminal, so
// the program really sees a terminal: raw mode, the size of the terminal,
// resizes, typed keys and the detach key all go through the real code path.
func TestAttachInARealTerminal(t *testing.T) {
	home := t.TempDir()
	stopAllAtEnd(t, home)
	addTestItem(t, home, "shell", "term", "size", "")
	mustOrch(t, home, "run", "term")

	p, err := pty.New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	cmd := p.Command(os.Args[0], "--home", home, "attach", "term")
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var screen syncBuffer
	go func() { _, _ = io.Copy(&screen, p) }()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	// The command reports its terminal size: attaching sends this terminal's size.
	waitFor(t, "the size of this terminal reaching the command", 15*time.Second, func() bool {
		return strings.Contains(screen.String(), "size:100x30;")
	})
	// Resizing this terminal is forwarded.
	if err := p.Resize(90, 20); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resize reaching the command", 15*time.Second, func() bool {
		return strings.Contains(screen.String(), "size:90x20;")
	})
	// The detach key (Ctrl+]) ends attach and leaves the run running.
	if _, err := p.Write([]byte{attach.DefaultDetachKey}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("attach did not end after the detach key; screen: %q", screen.String())
	}
	waitFor(t, "the detach message", 5*time.Second, func() bool {
		return strings.Contains(screen.String(), "detached from term - still running")
	})
	if out := mustOrch(t, home, "ls", "--running"); !strings.Contains(out, "term") {
		t.Fatal("detaching must leave the run running")
	}
}

func TestTagsGroupTheListAndSuggestFolders(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	mustOrch(t, home, "tag", "set", "shop", "--folder", root, "--color", "#4f8cff")
	if ls := mustOrch(t, home, "tag", "ls"); !strings.Contains(ls, "#4f8cff") || !strings.Contains(ls, root) {
		t.Fatalf("tag ls: %s", ls)
	}
	if code, _, _ := orch(t, home, "tag", "set", "shop", "--color", "blue"); code != 1 {
		t.Fatalf("a bad colour: exit %d", code)
	}
	code, _, errOut := orch(t, home, "add", "task", "Inside", "--alias", "inside", "--cmd", "echo hi", "--cwd", filepath.Join(root, "web"))
	if code != 0 || !strings.Contains(errOut, "suggested tag: shop") {
		t.Fatalf("add inside a tag folder suggests the tag: exit %d, %q", code, errOut)
	}
	if out := mustOrch(t, home, "ls", "--tag", "shop"); strings.Contains(out, "inside") {
		t.Fatal("a suggestion must not tag the item by itself")
	}
	mustOrch(t, home, "edit", "inside", "--add-tag", "shop")
	mustOrch(t, home, "add", "agent", "Claude", "--alias", "claude", "--cmd", "claude -c", "--tag", "shop,ai")
	mustOrch(t, home, "add", "task", "Loose", "--alias", "loose", "--cmd", "echo hi")
	mustOrch(t, home, "pin", "loose")

	grouped := mustOrch(t, home, "ls", "--group", "tag")
	iShop, iAI, iNone := strings.Index(grouped, "shop (2)"), strings.Index(grouped, "ai (1)"), strings.Index(grouped, "Untagged (1)")
	if iShop < 0 || iAI < 0 || iNone < iShop || iNone < iAI {
		t.Fatalf("grouped list (an item under each of its tags, untagged last): %s", grouped)
	}
	if out := mustOrch(t, home, "ls", "--pinned"); !strings.Contains(out, "loose *") || strings.Contains(out, "claude") {
		t.Fatalf("pinned: %s", out)
	}
	mustOrch(t, home, "tag", "rename", "shop", "store")
	if out := mustOrch(t, home, "ls", "--tag", "store"); !strings.Contains(out, "inside") || !strings.Contains(out, "claude") {
		t.Fatalf("rename moves the tag on items: %s", out)
	}
	mustOrch(t, home, "tag", "rm", "store", "--from-items")
	if out := mustOrch(t, home, "ls", "--tag", "store"); !strings.Contains(out, "no items") {
		t.Fatalf("rm --from-items: %s", out)
	}
}

func TestUserKinds(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "kind", "add", "MCP", "server", "--base", "service", "--color", "#db2777")
	mustOrch(t, home, "add", "mcp server", "Images", "--alias", "images", "--cmd", "node dist/index.js")
	mustOrch(t, home, "add", "service", "Web", "--alias", "web", "--cmd", "npm run dev")
	mustOrch(t, home, "kind", "set", "web", "mcp server")
	if code, _, _ := orch(t, home, "kind", "set", "web", "task"); code != 1 {
		t.Fatalf("a service cannot become a task: exit %d", code)
	}
	mustOrch(t, home, "kind", "edit", "task", "--name", "Jobs")
	ls := mustOrch(t, home, "kind", "ls")
	for _, want := range []string{"MCP server", "service", "2", "#db2777", "Jobs"} {
		if !strings.Contains(ls, want) {
			t.Fatalf("kind ls should show %q:\n%s", want, ls)
		}
	}
	if code, _, _ := orch(t, home, "kind", "rm", "service"); code != 1 {
		t.Fatalf("removing a built-in kind: exit %d", code)
	}
	mustOrch(t, home, "kind", "rm", "mcp server")
	if ls := mustOrch(t, home, "kind", "ls"); strings.Contains(ls, "MCP") {
		t.Fatalf("kind still listed:\n%s", ls)
	}
	if out := mustOrch(t, home, "ls", "--kind", "service"); !strings.Contains(out, "images") || !strings.Contains(out, "web") {
		t.Fatalf("items of a removed kind go back to their base:\n%s", out)
	}
}

func TestKindMove(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "kind", "add", "Alpha", "--base", "task")
	mustOrch(t, home, "kind", "add", "Beta", "--base", "task")
	mustOrch(t, home, "kind", "move", "beta", "up")
	ls := mustOrch(t, home, "kind", "ls")
	if strings.Index(ls, "Beta") > strings.Index(ls, "Alpha") {
		t.Fatalf("Beta should be first now:\n%s", ls)
	}
	if code, _, _ := orch(t, home, "kind", "move", "beta", "sideways"); code != 1 {
		t.Fatalf("bad direction: exit %d", code)
	}
}

func TestSchedulesFromTheCommandLine(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "add", "task", "Pull", "--alias", "pull", "--cmd", "git pull", "--schedule", "daily 09:00", "--missed", "catchup", "--timeout", "30")
	cat, err := catalog.New(home, nil).Load()
	if err != nil {
		t.Fatal(err)
	}
	tk := cat.Items[0].Task
	if tk.Schedule == nil || tk.Schedule.Spec() != "daily 09:00" || !tk.Schedule.Enabled || tk.Missed != "catchup" || tk.TimeoutMinutes != 30 {
		t.Fatalf("schedule: %+v %+v", tk, tk.Schedule)
	}
	mustOrch(t, home, "edit", "pull", "--schedule", "off", "--overlap", "queue")
	cat, _ = catalog.New(home, nil).Load()
	if tk := cat.Items[0].Task; tk.Schedule.Enabled || tk.Overlap != "queue" {
		t.Fatalf("off: %+v", tk)
	}
	mustOrch(t, home, "edit", "pull", "--schedule", "")
	if cat, _ = catalog.New(home, nil).Load(); cat.Items[0].Task.Schedule != nil {
		t.Fatal("an empty schedule removes it")
	}
	if code, _, _ := orch(t, home, "edit", "pull", "--schedule", "hourly"); code != 1 {
		t.Fatalf("bad schedule: exit %d", code)
	}
	if code, _, _ := orch(t, home, "add", "service", "Web", "--cmd", "x", "--schedule", "daily 09:00"); code != 1 {
		t.Fatalf("a service with a schedule: exit %d", code)
	}
	if code, _, _ := orch(t, home, "edit", "pull", "--missed", "sometimes"); code != 1 {
		t.Fatalf("bad missed policy: exit %d", code)
	}
}

func TestNotifyNeedsTheService(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "add", "task", "Job", "--alias", "job", "--cmd", "echo hi")
	if code, _, errOut := orch(t, home, "notify", "job", "done"); code == 0 || !strings.Contains(errOut, "not running") {
		t.Fatalf("notify without the service: exit %d, %q", code, errOut)
	}
}

func TestTrashAndExport(t *testing.T) {
	home := t.TempDir()
	mustOrch(t, home, "add", "task", "Job", "--alias", "job", "--cmd", "echo hi", "--env", "TOKEN=hush-hush")
	if out := mustOrch(t, home, "rm", "job"); !strings.Contains(out, "trash") {
		t.Fatalf("rm: %q", out)
	}
	if out := mustOrch(t, home, "trash"); !strings.Contains(out, "job") {
		t.Fatalf("trash ls: %q", out)
	}
	mustOrch(t, home, "trash", "restore", "job")
	if out := mustOrch(t, home, "ls"); !strings.Contains(out, "job") {
		t.Fatalf("restored: %q", out)
	}
	file := filepath.Join(t.TempDir(), "out.json")
	mustOrch(t, home, "export", "-o", file)
	b, err := os.ReadFile(file)
	if err != nil || strings.Contains(string(b), "hush-hush") || !strings.Contains(string(b), `"job"`) {
		t.Fatalf("export: %v %s", err, b)
	}
	other := t.TempDir()
	if out := mustOrch(t, other, "import", file); !strings.Contains(out, "imported 1 items") {
		t.Fatalf("import the export: %q", out)
	}
	mustOrch(t, home, "rm", "job", "--permanent")
	if out := mustOrch(t, home, "trash"); !strings.Contains(out, "empty") {
		t.Fatalf("permanent rm skips the trash: %q", out)
	}
}
