package runner

import (
	"bytes"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/testchild"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

// The test binary is the runner's host and the command the host runs, chosen
// by environment variables (see package testchild).
func TestMain(m *testing.M) {
	switch os.Getenv(testchild.EnvRole) {
	case "host":
		if err := host.Run(os.Getenv(testchild.EnvDir)); err != nil {
			os.Stderr.WriteString("host: " + err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	case "child":
		testchild.Main(os.Getenv(testchild.EnvMode), os.Getenv(testchild.EnvArg))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// newRunner returns a runner on a fresh home whose hosts are the test binary.
func newRunner(t *testing.T) *Runner {
	t.Helper()
	home := t.TempDir()
	r := New(home, catalog.New(home, nil), os.Args[0])
	r.HostEnv = func(dir string) []string {
		return append(os.Environ(), testchild.EnvRole+"=host", testchild.EnvDir+"="+dir)
	}
	t.Cleanup(func() { // never leave a host running
		runs, _ := r.AllRuns()
		for _, run := range runs {
			if run.State == StateRunning || run.State == StateHeld {
				_ = host.SendSignal(run.Dir, wire.SignalMsg{Signal: wire.SigKill})
				waitGone(t, run.Dir)
			}
		}
	})
	return r
}

func waitGone(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !host.Alive(dir) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("host in %s did not exit", dir)
}

// addItem adds an item whose command is the test binary in the given mode.
func addItem(t *testing.T, r *Runner, kind domain.Kind, alias, mode, arg string, edit func(*domain.Item)) domain.Item {
	t.Helper()
	var added domain.Item
	err := r.Store.Update(func(f *catalog.File) error {
		it := f.NewItem(kind, alias, alias, time.Now())
		it.Command = domain.Command{
			Shell: domain.ShellNone,
			Argv:  []string{os.Args[0]},
			Cwd:   t.TempDir(),
			Env: map[string]string{
				testchild.EnvRole: "child",
				testchild.EnvMode: mode,
				testchild.EnvArg:  arg,
			},
		}
		if edit != nil {
			edit(&it)
		}
		f.Items = append(f.Items, it)
		added = it
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return added
}

func (r *Runner) item(t *testing.T, ref string) domain.Item {
	t.Helper()
	cat, err := r.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	it, err := FindItem(cat.Items, ref)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// output reads from a run until the text contains want.
func output(t *testing.T, run Run, want string) string {
	t.Helper()
	c, err := host.Dial(run.Host, wire.HelloMsg{Client: "cli", Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var acc bytes.Buffer
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("waiting for %q: %v (got %q)", want, err, acc.String())
		}
		if f.Type == wire.Output {
			acc.Write(f.Payload)
			if bytes.Contains(acc.Bytes(), []byte(want)) {
				return acc.String()
			}
		}
	}
}

var grandchildRe = regexp.MustCompile(`grandchild:(\d+)[^\d]`)

// grandchildPID reads the pid the "tree" command announces.
func grandchildPID(t *testing.T, run Run) int {
	t.Helper()
	c, err := host.Dial(run.Host, wire.HelloMsg{Client: "cli", Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var acc bytes.Buffer
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("no grandchild announced: %v (got %q)", err, acc.String())
		}
		if f.Type != wire.Output {
			continue
		}
		acc.Write(f.Payload)
		if m := grandchildRe.FindSubmatch(acc.Bytes()); m != nil {
			pid, _ := strconv.Atoi(string(m[1]))
			return pid
		}
	}
}
