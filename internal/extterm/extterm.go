// Package extterm opens a command in the user's own terminal window. It is how
// the management window brings a run to the foreground: it starts
// "orchdock attach <run>" in Windows Terminal (or the console window,
// Terminal.app, or the desktop's terminal on other systems).
package extterm

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Open starts argv in a new terminal window or tab titled title. It does not
// wait for the terminal to close.
func Open(title string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("nothing to run")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = windowsCommand(title, argv)
	case "darwin":
		script := fmt.Sprintf(`tell application "Terminal" to do script %q`, shellLine(argv))
		cmd = exec.Command("osascript", "-e", script, "-e", `tell application "Terminal" to activate`)
	default:
		cmd = linuxCommand(title, argv)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open a terminal: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// windowsCommand prefers Windows Terminal and falls back to a console window.
func windowsCommand(title string, argv []string) *exec.Cmd {
	if wt, err := exec.LookPath("wt.exe"); err == nil {
		args := append([]string{"-w", "orchdock", "new-tab", "--title", title, "--suppressApplicationTitle", "--"}, argv...)
		return exec.Command(wt, args...)
	}
	return exec.Command("cmd.exe", append([]string{"/c", "start", title}, argv...)...)
}

func linuxCommand(title string, argv []string) *exec.Cmd {
	if p, err := exec.LookPath("x-terminal-emulator"); err == nil {
		return exec.Command(p, append([]string{"-e"}, argv...)...)
	}
	if p, err := exec.LookPath("gnome-terminal"); err == nil {
		return exec.Command(p, append([]string{"--title", title, "--"}, argv...)...)
	}
	return exec.Command("xterm", append([]string{"-T", title, "-e"}, argv...)...)
}

// shellLine joins argv for a POSIX shell.
func shellLine(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
