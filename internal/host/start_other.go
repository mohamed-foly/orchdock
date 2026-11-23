//go:build !windows

package host

import (
	"fmt"
	"os/exec"

	pty "github.com/aymanbagabas/go-pty"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/proctree"
)

func prepareProcess() error { return nil }

// startChild starts the command in the PTY. The PTY library makes the child a
// session leader, so its process id is also its process group id.
func startChild(p pty.Pty, spec domain.RunSpec) (*pty.Cmd, proctree.Tree, error) {
	exe, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		return nil, nil, fmt.Errorf("find %q: %w", spec.Argv[0], err)
	}
	cmd := p.Command(exe, spec.Argv[1:]...)
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %q: %w", exe, err)
	}
	return cmd, proctree.NewGroup(cmd.Process.Pid), nil
}

func selfInJob() bool { return false }
