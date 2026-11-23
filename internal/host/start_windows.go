//go:build windows

package host

import (
	"fmt"
	"os/exec"
	"syscall"

	pty "github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/proctree"
)

// prepareProcess clears the inherited "ignore Ctrl+C" flag. The host is
// started in a new process group, which sets that flag, and without this call
// a Ctrl+C written into the PTY would do nothing.
func prepareProcess() error { return proctree.ClearIgnoreCtrlC() }

// startChild starts the command in the PTY inside a Job Object.
//
// The child starts suspended, joins the job, and only then runs, so no
// grandchild can ever escape the job. The executable is resolved here because
// the PTY library resolves a bare name relative to Dir instead of PATH.
func startChild(p pty.Pty, spec domain.RunSpec) (*pty.Cmd, proctree.Tree, error) {
	exe, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		return nil, nil, fmt.Errorf("find %q: %w", spec.Argv[0], err)
	}
	job, err := proctree.NewJob()
	if err != nil {
		return nil, nil, err
	}
	cmd := p.Command(exe, spec.Argv[1:]...)
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if line, ok := cmdExeCommandLine(spec.Argv); ok {
		cmd.SysProcAttr.CmdLine = line
	}
	if err := cmd.Start(); err != nil {
		_ = job.Close()
		return nil, nil, fmt.Errorf("start %q: %w", exe, err)
	}
	pid := cmd.Process.Pid
	if err := job.Assign(pid); err != nil {
		_ = cmd.Process.Kill()
		_ = job.Close()
		return nil, nil, err
	}
	if _, err := proctree.ResumeProcess(pid); err != nil {
		_ = job.Terminate()
		_ = job.Close()
		return nil, nil, err
	}
	return cmd, job, nil
}

// selfInJob reports whether the host itself was started inside a job.
func selfInJob() bool { return proctree.SelfInJob() }
