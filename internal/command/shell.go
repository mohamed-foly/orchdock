package command

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

func goos() string { return runtime.GOOS }

func lookPath(name string) (string, error) { return exec.LookPath(name) }

// shellInfo is a concrete shell: its name, executable and quoting style.
type shellInfo struct {
	name    string
	exe     string
	dialect dialect
}

// pickShell resolves a shell name. Empty, "none" and "auto" choose the
// platform default: pwsh, else Windows PowerShell on Windows; the user's
// $SHELL (bash, zsh or sh), else sh elsewhere.
func pickShell(e Env, want string) (shellInfo, error) {
	name := want
	if name == "" || name == domain.ShellNone || name == "auto" {
		name = autoShell(e)
	}
	var d dialect
	switch name {
	case "pwsh", "powershell":
		d = dialectPowerShell
	case "cmd":
		d = dialectCmd
	case "bash", "zsh", "sh":
		d = dialectPosix
	default:
		return shellInfo{}, fmt.Errorf("%w: unknown shell %q", domain.ErrInvalid, name)
	}
	exe, err := e.LookPath(name)
	if err != nil {
		switch {
		case e.OS == "windows" && (name == "powershell" || name == "cmd"):
			exe = name + ".exe"
		case name == "sh":
			exe = "/bin/sh"
		default:
			return shellInfo{}, fmt.Errorf("%w: shell %q is not installed", domain.ErrNotFound, name)
		}
	}
	return shellInfo{name: name, exe: exe, dialect: d}, nil
}

func autoShell(e Env) string {
	if e.OS == "windows" {
		if _, err := e.LookPath("pwsh"); err == nil {
			return "pwsh"
		}
		return "powershell"
	}
	for _, kv := range e.Environ {
		if v, ok := strings.CutPrefix(kv, "SHELL="); ok {
			switch filepath.Base(v) {
			case "bash", "zsh", "sh":
				return filepath.Base(v)
			}
		}
	}
	return "sh"
}

// argv builds the program and arguments that run line (or an interactive
// shell when line is empty).
func (s shellInfo) argv(line string, loadProfile bool) []string {
	switch s.name {
	case "pwsh", "powershell":
		if line == "" {
			return []string{s.exe, "-NoLogo"}
		}
		a := []string{s.exe, "-NoLogo"}
		if !loadProfile {
			a = append(a, "-NoProfile")
		}
		return append(a, "-Command", line)
	case "cmd":
		if line == "" {
			return []string{s.exe, "/k"}
		}
		return []string{s.exe, "/d", "/s", "/c", line}
	default:
		if line == "" {
			return []string{s.exe, "-l"}
		}
		flag := "-c"
		if loadProfile {
			flag = "-lic"
		}
		return []string{s.exe, flag, line}
	}
}

// wrapShim runs Windows script shims the way the system would: batch files
// through cmd.exe and PowerShell scripts through PowerShell. npm installs
// its global commands as such shims. Other executables run directly.
//
// A batch file gets one cmd.exe line, the shim and its arguments quoted, which
// the host passes to cmd.exe as it is; separate arguments would break on a
// folder with a space (such as Program Files).
func (e Env) wrapShim(exe string, args []string) []string {
	if e.OS == "windows" {
		switch strings.ToLower(filepath.Ext(exe)) {
		case ".cmd", ".bat":
			return []string{"cmd.exe", "/d", "/s", "/c", dialectCmd.join(append([]string{exe}, args...))}
		case ".ps1":
			ps := "powershell.exe"
			if p, err := e.LookPath("pwsh"); err == nil {
				ps = p
			}
			return append([]string{ps, "-NoProfile", "-File", exe}, args...)
		}
	}
	return append([]string{exe}, args...)
}
