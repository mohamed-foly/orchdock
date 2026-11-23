// Package procs lists OS processes, reads their command lines and checks
// whether a PID is alive. OrchDock uses it only for its own processes (alive
// check) and to name the holder of a busy port; it never guesses what other
// programs are doing.
package procs

import (
	"context"
	"strings"
)

// Process is one OS process with its command line.
type Process struct {
	PID         int
	PPID        int
	Name        string // executable file name, e.g. "node.exe"
	CommandLine string // empty when it could not be read
}

// Lister lists processes.
type Lister interface {
	// List returns processes whose executable name is in names
	// (case-insensitive); an empty names lists every process.
	List(ctx context.Context, names []string) ([]Process, error)
}

// Tokenize splits a command line into arguments, honouring double quotes.
func Tokenize(cmd string) []string {
	var out []string
	var cur strings.Builder
	inQuote, has := false, false
	for _, r := range cmd {
		switch {
		case r == '"':
			inQuote = !inQuote
			has = true
		case (r == ' ' || r == '\t') && !inQuote:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

// nameFilter returns a matcher for names (nil names = match all).
func nameFilter(names []string) func(string) bool {
	if len(names) == 0 {
		return func(string) bool { return true }
	}
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	return func(name string) bool { return want[strings.ToLower(name)] }
}
