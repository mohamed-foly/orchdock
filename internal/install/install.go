// Package install puts OrchDock in a per-user program folder with a
// Start-menu entry, the folder on PATH, the short alias odock and, if asked,
// a start at sign-in; Uninstall takes all of it back. Data is never touched:
// it stays in the per-user data folder (or wherever it already was).
package install

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Programs are the programs of an installation, without the .exe suffix.
var Programs = []string{"orchdock", "orchdockw"}

// Alias is the short name installed next to the command line program.
const Alias = "odock"

// Options describe an installation.
type Options struct {
	From      string // folder holding the programs to install
	To        string // program folder
	Home      string // data folder written to .env as ORCHDOCK_HOME
	Version   string
	Autostart bool // start in the tray at sign-in
}

// Step is one thing done (or skipped), shown to the user.
type Step struct {
	Text string
	Err  error
}

// Report lists the steps of an install or uninstall.
type Report struct{ Steps []Step }

func (r *Report) add(text string, err error) { r.Steps = append(r.Steps, Step{text, err}) }

// Err is the first failed step, if any.
func (r Report) Err() error {
	var errs []error
	for _, s := range r.Steps {
		if s.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Text, s.Err))
		}
	}
	return errors.Join(errs...)
}

// ErrUnsupported is returned where installing is not available yet.
var ErrUnsupported = errors.New("install is available on Windows only for now; put the program in a folder on your PATH instead")

// SetEnvLine sets KEY=value in the .env file at path, keeping every other
// line; an empty value removes the key. The file is removed when nothing is
// left in it.
func SetEnvLine(path, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var out bytes.Buffer
	done := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		k, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(strings.TrimPrefix(k, "export ")) == key {
			if value != "" && !done {
				fmt.Fprintf(&out, "%s=%s\n", key, value)
				done = true
			}
			continue
		}
		out.WriteString(line + "\n")
	}
	if value != "" && !done {
		fmt.Fprintf(&out, "%s=%s\n", key, value)
	}
	if strings.TrimSpace(out.String()) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, out.Bytes(), 0o600)
}
