//go:build !windows

package host

import "os/exec"

func canBreakaway() bool { return false }

func applyBreakaway(*exec.Cmd) {}
