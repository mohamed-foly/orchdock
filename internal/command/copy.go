package command

import (
	"fmt"
	"strings"
)

// CopyText returns the command as text to paste into a terminal.
//
// The full form changes to the working directory, sets the item's environment
// and then runs the command, written for the shell that runs it (for a direct
// command, the platform's default shell). The bare form is only the command.
//
// Secret environment values are never included: they appear as "<set NAME>"
// so the pasted text shows what is missing. Variables loaded from an env file
// are not included either.
func (r Resolved) CopyText(bare bool) string {
	text := r.text
	if text == "" {
		text = r.Shell // an interactive shell item: just the shell
	}
	if bare {
		return text
	}
	var steps []string
	switch r.dialect {
	case dialectPowerShell:
		steps = append(steps, "Set-Location -LiteralPath "+r.dialect.quoteAlways(r.Cwd))
		for _, k := range sortedKeys(r.set) {
			steps = append(steps, fmt.Sprintf("$env:%s = %s", k, r.dialect.quoteAlways(r.value(k))))
		}
		steps = append(steps, text)
		return strings.Join(steps, "; ")
	case dialectCmd:
		steps = append(steps, "cd /d "+r.dialect.quoteAlways(r.Cwd))
		for _, k := range sortedKeys(r.set) {
			steps = append(steps, fmt.Sprintf("set %s", r.dialect.quoteAlways(k+"="+r.value(k))))
		}
	default:
		steps = append(steps, "cd "+r.dialect.quoteAlways(r.Cwd))
		for _, k := range sortedKeys(r.set) {
			steps = append(steps, fmt.Sprintf("export %s=%s", k, r.dialect.quoteAlways(r.value(k))))
		}
	}
	steps = append(steps, text)
	return strings.Join(steps, " && ")
}

// value is the text shown for an environment variable in Copy output.
func (r Resolved) value(name string) string {
	if r.secret[name] {
		return "<set " + name + ">"
	}
	return r.set[name]
}
