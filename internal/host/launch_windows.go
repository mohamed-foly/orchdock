//go:build windows

package host

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/mohamed-foly/orchdock/internal/proctree"
)

// canBreakaway reports whether this process runs inside a job, in which case
// the host asks to leave it.
func canBreakaway() bool { return proctree.SelfInJob() }

func applyBreakaway(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_BREAKAWAY_FROM_JOB
}
