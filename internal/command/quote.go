package command

import (
	"regexp"
	"strings"
)

// dialect is the quoting style of a shell.
type dialect int

const (
	dialectPosix dialect = iota // bash, zsh, sh
	dialectPowerShell
	dialectCmd
)

var (
	safePosix = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
	safePS    = regexp.MustCompile(`^[A-Za-z0-9_@%+=:./\\-]+$`)
	safeCmd   = regexp.MustCompile(`^[A-Za-z0-9_@+=:,./\\-]+$`)
)

// quote returns s quoted for the dialect, leaving plain words as they are.
func (d dialect) quote(s string) string {
	switch d {
	case dialectPowerShell:
		if safePS.MatchString(s) {
			return s
		}
		return d.quoteAlways(s)
	case dialectCmd:
		if safeCmd.MatchString(s) {
			return s
		}
		return d.quoteAlways(s)
	default:
		if safePosix.MatchString(s) {
			return s
		}
		return d.quoteAlways(s)
	}
}

// quoteAlways quotes s even when it is a plain word.
//
// cmd.exe has no complete quoting: inside double quotes %VAR% is still
// expanded, and embedded quotes are escaped the way programs parse them
// (\"). It is best effort and documented as such.
func (d dialect) quoteAlways(s string) string {
	switch d {
	case dialectPowerShell:
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case dialectCmd:
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	default:
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
}

// join quotes every word and joins them with spaces. In PowerShell a quoted
// first word needs the call operator.
func (d dialect) join(words []string) string {
	if len(words) == 0 {
		return ""
	}
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = d.quote(w)
	}
	line := strings.Join(out, " ")
	if d == dialectPowerShell && out[0] != words[0] {
		line = "& " + line
	}
	return line
}

// insertAfterFirstWord puts extra after the first word of line (the
// executable). A first word may be quoted with ' or ".
func insertAfterFirstWord(line, extra string) string {
	if extra == "" {
		return line
	}
	s := strings.TrimLeft(line, " \t")
	lead := line[:len(line)-len(s)]
	end := firstWordEnd(s)
	if end >= len(s) {
		return lead + s + " " + extra
	}
	return lead + s[:end] + " " + extra + s[end:]
}

func firstWordEnd(s string) int {
	if s == "" {
		return 0
	}
	if q := s[0]; q == '\'' || q == '"' {
		if i := strings.IndexByte(s[1:], q); i >= 0 {
			return i + 2
		}
		return len(s)
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return i
	}
	return len(s)
}
