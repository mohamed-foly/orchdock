package detach

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// TestDetachedChildRuns starts the test binary itself detached and checks that it runs to completion.
func TestDetachedChildRuns(t *testing.T) {
	if os.Getenv("ORCHDOCK_DETACH_CHILD") != "" {
		os.Exit(7)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{Console, GUI} {
		cmd := exec.Command(exe, "-test.run=^TestDetachedChildRuns$")
		cmd.Env = append(os.Environ(), "ORCHDOCK_DETACH_CHILD=1")
		Apply(cmd, kind)
		if cmd.SysProcAttr == nil {
			t.Fatal("SysProcAttr not set")
		}
		err := cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 7 {
			t.Fatalf("kind %d: want exit code 7, got %v", kind, err)
		}
	}
}
