//go:build windows

package detach

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Flags returns the process creation flags used for kind.
func Flags(kind Kind) uint32 {
	if kind == GUI {
		return windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS
	}
	// CREATE_NO_WINDOW is ignored together with DETACHED_PROCESS, so the two are never combined.
	return windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW
}

// Apply detaches cmd from this process's console and Ctrl+C group.
func Apply(cmd *exec.Cmd, kind Kind) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= Flags(kind)
}
