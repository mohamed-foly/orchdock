// Package autostart starts OrchDock (tray only) when the user signs in, so
// scheduled tasks run without opening it by hand.
package autostart

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Supported reports whether this platform can start OrchDock at sign-in.
const Supported = true

// runKey is the per-user list of programs started at sign-in (tests point it elsewhere).
var runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

const value = "OrchDock"

// Command is what runs at sign-in: the windowless program, tray only.
func Command(exe string) string { return fmt.Sprintf(`"%s" ui --tray`, exe) } // not %q: it would double the backslashes

// Enabled reports whether a sign-in entry exists and what it runs.
func Enabled() (bool, string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(value)
	if errors.Is(err, registry.ErrNotExist) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, v, nil
}

// Set adds (with exe) or removes the sign-in entry. It touches only the
// current user's own entry, never other programs' ones.
func Set(on bool, exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		err := k.DeleteValue(value)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(exe) == "" {
		return errors.New("no program to start")
	}
	return k.SetStringValue(value, Command(exe))
}
