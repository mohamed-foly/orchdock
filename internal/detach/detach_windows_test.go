//go:build windows

package detach

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestFlags(t *testing.T) {
	c := Flags(Console)
	if c&windows.CREATE_NO_WINDOW == 0 || c&windows.DETACHED_PROCESS != 0 || c&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("console flags %#x", c)
	}
	g := Flags(GUI)
	if g&windows.DETACHED_PROCESS == 0 || g&windows.CREATE_NO_WINDOW != 0 {
		t.Fatalf("gui flags %#x", g)
	}
	cmd := exec.Command("x")
	Apply(cmd, GUI)
	if cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow must never be set")
	}
}
