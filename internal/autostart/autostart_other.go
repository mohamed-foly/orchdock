//go:build !windows

// Package autostart starts OrchDock (tray only) when the user signs in.
package autostart

import "errors"

// Supported reports whether this platform can start OrchDock at sign-in.
const Supported = false

var errUnsupported = errors.New("starting at sign-in is not available on this system yet")

// Command is what would run at sign-in.
func Command(exe string) string { return exe + " ui --tray" }

// Enabled reports whether a sign-in entry exists.
func Enabled() (bool, string, error) { return false, "", nil }

// Set is not available on this system yet.
func Set(bool, string) error { return errUnsupported }
