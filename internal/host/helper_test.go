package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/detach"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/testchild"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

// The test binary plays three roles, chosen by environment variables: the
// test runner (default), a host (ORCHDOCK_TEST_ROLE=host) and the command a
// host runs (ORCHDOCK_TEST_ROLE=child). This avoids building helper programs.
const (
	envRole = "ORCHDOCK_TEST_ROLE"
	envDir  = "ORCHDOCK_TEST_DIR"
	envMode = "ORCHDOCK_TEST_MODE"
	envArg  = "ORCHDOCK_TEST_ARG"
)

func TestMain(m *testing.M) {
	switch os.Getenv(envRole) {
	case "host":
		if err := Run(os.Getenv(envDir)); err != nil {
			fmt.Fprintln(os.Stderr, "host:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "child":
		childMain(os.Getenv(envMode), os.Getenv(envArg))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func childMain(mode, arg string) { testchild.Main(mode, arg) }

type run struct {
	t    *testing.T
	dir  string
	info Info
}

// startRun writes a spec for the test binary acting as the child, launches a
// detached host (also the test binary) and waits until it answers.
func startRun(t *testing.T, mode, arg string, edit func(*domain.RunSpec)) *run {
	t.Helper()
	dir := t.TempDir()
	spec := domain.RunSpec{
		RunID:     "r_" + fmt.Sprintf("%010d", time.Now().UnixNano()%1e10),
		ItemID:    domain.AdhocItemID,
		Trigger:   domain.TriggerManual,
		Argv:      []string{os.Args[0]},
		Cwd:       dir,
		Env:       append(os.Environ(), envRole+"=child", envMode+"="+mode, envArg+"="+arg),
		Cols:      100,
		Rows:      30,
		CreatedAt: time.Now().UTC(),
	}
	if edit != nil {
		edit(&spec)
	}
	b, _ := json.Marshal(spec)
	if err := os.WriteFile(filepath.Join(dir, SpecFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := Launch(LaunchOptions{
		Exe:    os.Args[0],
		RunDir: dir,
		Env:    append(os.Environ(), envRole+"=host", envDir+"="+dir),
		Kind:   detach.Console,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	r := &run{t: t, dir: dir, info: info}
	t.Cleanup(func() {
		if Alive(dir) {
			_ = SendSignal(dir, wire.SignalMsg{Signal: wire.SigKill})
			r.waitExit(10 * time.Second)
		}
	})
	return r
}

func (r *run) dial(client string) *Conn {
	r.t.Helper()
	c, err := Dial(r.info, wire.HelloMsg{Client: client, Cols: 100, Rows: 30})
	if err != nil {
		r.t.Fatalf("dial: %v", err)
	}
	r.t.Cleanup(func() { c.Close() })
	return c
}

// waitExit waits until the host has removed host.json and returns the result.
func (r *run) waitExit(d time.Duration) (Result, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(r.dir, InfoFile)); os.IsNotExist(err) {
			res, err := ReadResult(r.dir)
			return res, err == nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return Result{}, false
}

// readUntil reads output frames until the accumulated text contains want.
func readUntil(t *testing.T, c *Conn, want string, d time.Duration) string {
	t.Helper()
	var acc bytes.Buffer
	_ = c.SetReadDeadline(time.Now().Add(d))
	defer c.SetReadDeadline(time.Time{})
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("waiting for %q: %v (got %q)", want, err, acc.String())
		}
		if f.Type == wire.Output {
			acc.Write(f.Payload)
			if strings.Contains(acc.String(), want) {
				return acc.String()
			}
		}
	}
}

// replay returns the output sent before REPLAY_END.
func replay(t *testing.T, c *Conn) string {
	t.Helper()
	var acc bytes.Buffer
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		switch f.Type {
		case wire.Output:
			acc.Write(f.Payload)
		case wire.ReplayEnd:
			return acc.String()
		}
	}
}

// readMatch reads output until re matches the accumulated text and returns the
// submatches. Terminal output contains cursor-control sequences, so tests match
// patterns instead of whole lines.
func readMatch(t *testing.T, c *Conn, re *regexp.Regexp, d time.Duration) []string {
	t.Helper()
	var acc bytes.Buffer
	_ = c.SetReadDeadline(time.Now().Add(d))
	defer c.SetReadDeadline(time.Time{})
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("waiting for %v: %v (got %q)", re, err, acc.String())
		}
		if f.Type == wire.Output {
			acc.Write(f.Payload)
			if m := re.FindStringSubmatch(acc.String()); m != nil {
				return m
			}
		}
	}
}

var grandchildRe = regexp.MustCompile(`grandchild:(\d+)[^\d]`)

// grandchildPID waits for the tree child's announcement and returns the pid.
func grandchildPID(t *testing.T, c *Conn) int {
	t.Helper()
	m := readMatch(t, c, grandchildRe, 10*time.Second)
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}
