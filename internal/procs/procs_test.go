package procs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := map[string][]string{
		`"X:\Program Files\Tool\tool.exe" --dir="X:\data\my dir" run`: {`X:\Program Files\Tool\tool.exe`, `--dir=X:\data\my dir`, "run"},
		"node  server.js\t--port 7401":                                {"node", "server.js", "--port", "7401"},
		`cmd /c ""`:                                                   {"cmd", "/c", ""},
		"":                                                            nil,
	}
	for in, want := range tests {
		if got := Tokenize(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Tokenize(%q) = %q want %q", in, got, want)
		}
	}
}

func TestAliveSelfAndExited(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("own process should be alive")
	}
	if Alive(0) || Alive(-1) {
		t.Fatal("invalid pids must not be alive")
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if Alive(cmd.Process.Pid) {
		t.Fatalf("exited child %d reported alive", cmd.Process.Pid)
	}
}

func TestListFindsSelfWithCommandLine(t *testing.T) {
	exe, _ := os.Executable()
	name := filepath.Base(exe)
	list, err := New().List(context.Background(), []string{strings.ToUpper(name)})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.PID == os.Getpid() {
			if !strings.Contains(p.CommandLine, "test") {
				t.Fatalf("command line not read: %q", p.CommandLine)
			}
			return
		}
	}
	t.Fatalf("own pid %d not listed among %d processes named %s", os.Getpid(), len(list), name)
}
