//go:build !windows

package procs

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
)

type psLister struct{}

// New returns the POSIX Lister (ps(1)).
func New() Lister { return psLister{} }

// List implements Lister.
func (psLister) List(ctx context.Context, names []string) ([]Process, error) {
	b, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, err
	}
	return parsePS(string(b), names), nil
}

// Alive reports whether pid is a running process (zombies count as exited
// only once reaped by their parent).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
