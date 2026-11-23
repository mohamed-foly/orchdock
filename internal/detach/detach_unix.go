//go:build !windows

package detach

import (
	"os/exec"
	"syscall"
)

// Apply starts cmd in its own session so it outlives this process and its terminal.
func Apply(cmd *exec.Cmd, _ Kind) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
