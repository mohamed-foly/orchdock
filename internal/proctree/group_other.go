//go:build !windows

package proctree

import (
	"errors"
	"syscall"
)

// Group is a process group led by the run's child process.
type Group struct{ pgid int }

// NewGroup returns the tree led by the process pgid. The PTY library makes the
// child a session leader, so its process id is also its group id.
func NewGroup(pgid int) *Group { return &Group{pgid: pgid} }

func (g *Group) signal(sig syscall.Signal) error {
	err := syscall.Kill(-g.pgid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil // nobody left
	}
	return err
}

// Terminate sends SIGTERM to the group.
func (g *Group) Terminate() error { return g.signal(syscall.SIGTERM) }

// Kill sends SIGKILL to the group.
func (g *Group) Kill() error { return g.signal(syscall.SIGKILL) }

// Close kills what is left of the group.
func (g *Group) Close() error { return g.Kill() }

// SelfInJob is always false outside Windows.
func SelfInJob() bool { return false }

// ClearIgnoreCtrlC does nothing outside Windows.
func ClearIgnoreCtrlC() error { return nil }
