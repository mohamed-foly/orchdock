package host

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/detach"
)

// LaunchOptions describe how to start a host.
type LaunchOptions struct {
	Exe     string      // the OrchDock binary that runs the host
	Args    []string    // arguments before the run directory, normally {"host"}
	RunDir  string      // contains spec.json
	Env     []string    // nil inherits this process's environment
	Kind    detach.Kind // Console for orchdock, GUI for the windowless orchdockw
	Timeout time.Duration
}

// Launch starts a detached host for opts.RunDir and waits until it answers.
// The host outlives the caller. On failure the error includes the end of the
// host's own log so the reason is visible.
func Launch(o LaunchOptions) (Info, error) {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	_ = os.Remove(filepath.Join(o.RunDir, InfoFile))
	_ = os.Remove(filepath.Join(o.RunDir, ResultFile))

	args := append(append([]string{}, o.Args...), o.RunDir)
	start := func(breakaway bool) (*exec.Cmd, error) {
		cmd := exec.Command(o.Exe, args...)
		cmd.Env = o.Env
		detach.Apply(cmd, o.Kind) // stdio stays nil: the null device
		if breakaway {
			applyBreakaway(cmd)
		}
		return cmd, cmd.Start()
	}
	// Leave a job the launcher may run in, so the host is not killed with it.
	cmd, err := start(canBreakaway())
	if err != nil && canBreakaway() {
		cmd, err = start(false)
	}
	if err != nil {
		return Info{}, fmt.Errorf("start host: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.Now().Add(o.Timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return Info{}, fmt.Errorf("host exited early (%v)%s", err, hostLogTail(o.RunDir))
		default:
		}
		if info, err := ReadInfo(o.RunDir); err == nil && Ping(info) == nil {
			return info, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return Info{}, fmt.Errorf("host did not answer within %s%s", o.Timeout, hostLogTail(o.RunDir))
}

// hostLogTail returns the last lines of host.log for error messages.
func hostLogTail(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, HostLogFile))
	if err != nil || len(b) == 0 {
		return ""
	}
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return "\nhost log:\n" + strings.TrimSpace(string(bytes.Join(lines, []byte("\n"))))
}
