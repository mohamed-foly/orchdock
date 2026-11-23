package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/mohamed-foly/orchdock/internal/config"
	"github.com/mohamed-foly/orchdock/internal/runner"
)

func init() {
	register("doctor", "", "check that everything OrchDock needs is in place", cmdDoctor)
}

// check results.
const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "FAIL"
)

func cmdDoctor(e *Env, _ []string) error {
	failed := 0
	report := func(status, format string, a ...any) {
		fmt.Fprintf(e.Out, "[%-4s] %s\n", status, fmt.Sprintf(format, a...))
		if status == checkFail {
			failed++
		}
	}

	// The data folder must be writable.
	if err := os.MkdirAll(e.Home, 0o700); err != nil {
		report(checkFail, "data folder %s cannot be created: %v", e.Home, err)
	} else if f, err := os.CreateTemp(e.Home, ".doctor-*"); err != nil {
		report(checkFail, "data folder %s is not writable: %v", e.Home, err)
	} else {
		_ = f.Close()
		_ = os.Remove(f.Name())
		report(checkOK, "data folder %s is writable", e.Home)
	}

	if cat, err := e.Store.Load(); err != nil {
		report(checkFail, "catalog cannot be read: %v", err)
	} else {
		report(checkOK, "catalog: %d items, %d tag definitions", len(cat.Items), len(cat.Tags))
	}
	if _, err := config.Load(e.Home); err != nil {
		report(checkWarn, "settings cannot be read (defaults are used): %v", err)
	} else {
		report(checkOK, "settings are valid")
	}

	// Shells that items can use.
	shells := []string{"bash", "zsh", "sh"}
	if runtime.GOOS == "windows" {
		shells = []string{"pwsh", "powershell", "cmd"}
	}
	for _, s := range shells {
		if p, err := exec.LookPath(s); err == nil {
			report(checkOK, "shell %s: %s", s, p)
		} else {
			report(checkWarn, "shell %s was not found on PATH", s)
		}
	}
	// Programs OrchDock is usually used with. None is required.
	for _, p := range []string{"git", "node", "claude", "codex", "backlog"} {
		if path, err := exec.LookPath(p); err == nil {
			report(checkOK, "%s: %s", p, path)
		} else {
			report(checkWarn, "%s was not found on PATH (only needed if you run it)", p)
		}
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("wt.exe"); err == nil {
			report(checkOK, "Windows Terminal: %s", p)
		} else {
			report(checkWarn, "Windows Terminal (wt.exe) was not found; terminals open in the console window instead")
		}
	}

	// Runs that lost their host.
	runs, _ := e.Runner.AllRuns()
	running := 0
	for _, r := range runs {
		if r.State == runner.StateRunning {
			running++
		}
	}
	report(checkOK, "%d runs are running", running)
	if n := len(e.Runner.Interrupted()); n > 0 {
		report(checkWarn, "%d runs were interrupted (the machine restarted?) and can be restored", n)
	}

	if failed > 0 {
		return fmt.Errorf("%d checks failed", failed)
	}
	return nil
}
