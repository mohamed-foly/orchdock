//go:build windows

package host

import (
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// cmdExeCommandLine returns the raw command line for argv of the form
// `cmd.exe ... /s /c <line>` (or /k), and false for anything else.
//
// cmd.exe does not parse its command line the way other programs do: the
// usual escaping (`"` becomes `\"`) reaches it literally, so `echo "a b"`
// would print `\"a b\"` and a quoted program path would not be found. With
// /s, cmd.exe removes the first and last quote of what follows /c and runs
// the rest as typed, so the line is passed as `"<line>"`, untouched.
func cmdExeCommandLine(argv []string) (string, bool) {
	n := len(argv)
	if n < 4 || !strings.EqualFold(strings.TrimSuffix(strings.ToLower(filepath.Base(argv[0])), ".exe"), "cmd") {
		return "", false
	}
	if sw := strings.ToLower(argv[n-2]); sw != "/c" && sw != "/k" {
		return "", false
	}
	if !slices.ContainsFunc(argv[1:n-2], func(a string) bool { return strings.EqualFold(a, "/s") }) {
		return "", false
	}
	return windows.ComposeCommandLine(argv[:n-1]) + ` "` + argv[n-1] + `"`, true
}
