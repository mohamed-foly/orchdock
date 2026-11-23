//go:build !windows

package install

import (
	"os"
	"path/filepath"
)

// Supported reports whether Install works on this system.
const Supported = false

// DefaultDir is where the program would be installed.
func DefaultDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "bin")
}

// InstalledAt reports an existing installation (none on this system).
func InstalledAt() (string, bool) { return "", false }

// Install is not available on this system yet.
func Install(Options) Report {
	var r Report
	r.add("install", ErrUnsupported)
	return r
}

// Uninstall is not available on this system yet.
func Uninstall(string) (Report, []string) {
	var r Report
	r.add("uninstall", ErrUnsupported)
	return r, nil
}

// DeleteLater is not available on this system yet.
func DeleteLater(string, []string) error { return ErrUnsupported }
