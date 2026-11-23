package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/install"
	"github.com/mohamed-foly/orchdock/internal/paths"
	"github.com/mohamed-foly/orchdock/internal/runner"
	"github.com/mohamed-foly/orchdock/internal/server"
)

func init() {
	register("install", "[--dir folder] [--autostart] [--no-start]",
		"install for this user: Start menu, PATH, the odock alias, optional start at sign-in", cmdInstall)
	register("uninstall", "[--yes] [--stop-runs]", "remove what install added; your data is kept", cmdUninstall)
}

func cmdInstall(e *Env, args []string) error {
	fs := newFlags("install", e.Err)
	dir := fs.String("dir", "", "program folder (default: your Programs folder)")
	auto := fs.Bool("autostart", false, "start OrchDock in the tray when you sign in")
	noStart := fs.Bool("no-start", false, "do not start OrchDock after installing")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 0, 0, "install [--dir folder] [--autostart] [--no-start]"); err != nil {
		return err
	}
	if !install.Supported {
		return install.ErrUnsupported
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	from := filepath.Dir(self)
	if !fileExists(filepath.Join(from, "orchdockw.exe")) {
		return fmt.Errorf("%w: orchdockw.exe must be next to %s; install from the unpacked release folder", domain.ErrNotFound, filepath.Base(self))
	}
	to := *dir
	if to == "" {
		if d, ok := install.InstalledAt(); ok {
			to = d
		} else {
			to = install.DefaultDir()
		}
	}
	if to, err = filepath.Abs(to); err != nil {
		return err
	}
	// keep data that already exists; otherwise use the per-user data folder
	home := e.Home
	if !fileExists(filepath.Join(home, "catalog.json")) {
		home = filepath.Join(os.Getenv("LOCALAPPDATA"), paths.AppName)
	}
	if _, alive := server.ReadInfo(e.Home); alive && !strings.EqualFold(from, to) {
		if err := cmdQuit(e, nil); err != nil {
			return err
		}
	}
	rep := install.Install(install.Options{From: from, To: to, Home: home, Version: Version, Autostart: *auto})
	printSteps(e, rep)
	if err := rep.Err(); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "\nOrchDock is installed in %s; its data is in %s.\n", to, home)
	fmt.Fprintf(e.Out, "Open a new terminal to use orchdock (or %s) from anywhere.\n", install.Alias)
	if !*noStart {
		cmd := exec.Command(filepath.Join(to, "orchdockw.exe"), "ui", "--tray")
		cmd.Dir = to
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("installed, but could not start it: %w", err)
		}
		_ = cmd.Process.Release()
		fmt.Fprintln(e.Out, "OrchDock is starting in the tray.")
	}
	return nil
}

func cmdUninstall(e *Env, args []string) error {
	fs := newFlags("uninstall", e.Err)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	stopRuns := fs.Bool("stop-runs", false, "stop running items first")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 0, 0, "uninstall [--yes] [--stop-runs]"); err != nil {
		return err
	}
	if !install.Supported {
		return install.ErrUnsupported
	}
	dir, ok := install.InstalledAt()
	if !ok {
		return fmt.Errorf("%w: OrchDock is not installed for this user (a portable copy is removed by deleting its folder)", domain.ErrNotFound)
	}
	var live []string
	if runs, err := e.Runner.AllRuns(); err == nil {
		for _, r := range runs {
			if r.State == runner.StateRunning || r.State == runner.StateHeld {
				live = append(live, aliasOf(e, r.ItemID))
			}
		}
	}
	if len(live) > 0 && !*stopRuns {
		return fmt.Errorf("%w: still running: %s; stop them first or use --stop-runs", domain.ErrConflict, strings.Join(live, ", "))
	}
	if !*yes {
		fmt.Fprintf(e.Out, "Remove OrchDock from %s? Your data in %s is kept. [y/N] ", dir, e.Home)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return errors.New("cancelled")
		}
	}
	if len(live) > 0 {
		if err := cmdStop(e, []string{"--all"}); err != nil {
			return err
		}
	}
	if err := cmdQuit(e, nil); err != nil {
		return err
	}
	rep, files := install.Uninstall(dir)
	printSteps(e, rep)
	self, _ := os.Executable()
	if strings.HasPrefix(strings.ToLower(self), strings.ToLower(dir)+string(filepath.Separator)) {
		if err := install.DeleteLater(dir, files); err != nil {
			return err
		}
	} else {
		for _, f := range files {
			_ = os.Remove(f)
		}
		_ = os.Remove(dir)
	}
	fmt.Fprintf(e.Out, "\nOrchDock is removed. Your data is still in %s; delete that folder to remove it too.\n", e.Home)
	return rep.Err()
}

func printSteps(e *Env, rep install.Report) {
	for _, s := range rep.Steps {
		if s.Err != nil {
			fmt.Fprintf(e.Out, "  ✗ %s: %v\n", s.Text, s.Err)
		} else {
			fmt.Fprintf(e.Out, "  ✓ %s\n", s.Text)
		}
	}
}
