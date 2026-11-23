package domain

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
)

// Shells an item may use. "none" runs Argv directly; "auto" picks the
// platform default (pwsh → powershell on Windows, $SHELL → /bin/sh on Unix).
var Shells = []string{"auto", "pwsh", "powershell", "cmd", "bash", "zsh", "sh", "none"}

// ShellNone means "run argv directly, no shell".
const ShellNone = "none"

// Command says what an item runs. Either Line (run through Shell) or Argv
// (Shell == "none") is set; shell items may leave both empty for an
// interactive shell; agent items take their command from the agent profile.
type Command struct {
	Shell          string            `json:"shell,omitempty"`
	Line           string            `json:"line,omitempty"`
	Argv           []string          `json:"argv,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	EnvFile        string            `json:"envFile,omitempty"`
	SecretEnv      []string          `json:"secretEnv,omitempty"` // names of Env entries whose values the Copy button never reveals
	LoadProfile    bool              `json:"loadProfile,omitempty"`
	Variants       []Variant         `json:"variants,omitempty"`
	DefaultVariant string            `json:"defaultVariant,omitempty"`
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (c *collector) env(field string, env map[string]string) {
	for _, k := range sortedKeys(env) {
		if !envKeyRe.MatchString(k) {
			c.addf(field, "%q is not a valid environment variable name", k)
		}
	}
	c.placeholdersMap(field, env)
}

func (c *collector) command(field string, kind Kind, cmd Command) {
	s := c.sub(field)
	defer c.merge(s)
	if cmd.Shell != "" && !slices.Contains(Shells, cmd.Shell) {
		s.addf("shell", "%q is not one of %v", cmd.Shell, Shells)
	}
	if cmd.Line != "" && cmd.Argv != nil {
		s.addf("", "set either line or argv, not both")
	}
	if cmd.Shell == ShellNone && cmd.Line != "" {
		s.addf("line", "shell \"none\" runs argv directly; use argv or choose a shell")
	}
	if cmd.Shell != ShellNone && cmd.Argv != nil {
		s.addf("argv", "argv needs shell \"none\"; use line for shell commands")
	}
	for i, a := range cmd.Argv {
		if i == 0 && a == "" {
			s.addf("argv", "the executable must not be empty")
		}
	}
	switch kind {
	case KindLink:
		if cmd.Line != "" || cmd.Argv != nil {
			s.addf("", "%s items do not have their own command line", kind)
		}
	case KindService, KindTask:
		if cmd.Line == "" && len(cmd.Argv) == 0 {
			s.addf("", "a %s needs a command (line, or argv with shell \"none\")", kind)
		}
	case KindShell:
		if cmd.Shell == ShellNone && len(cmd.Argv) == 0 {
			s.addf("argv", "shell \"none\" needs argv")
		}
	}
	s.placeholders("line", cmd.Line)
	s.placeholders("argv", cmd.Argv...)
	s.placeholders("cwd", cmd.Cwd)
	s.placeholders("envFile", cmd.EnvFile)
	s.env("env", cmd.Env)
	for i, name := range cmd.SecretEnv {
		if !envKeyRe.MatchString(name) {
			s.addf(fmt.Sprintf("secretEnv[%d]", i), "%q is not a valid environment variable name", name)
		}
	}
	s.variants("variants", cmd.Variants)
	if cmd.DefaultVariant != "" && !ValidVariantID(cmd.DefaultVariant) {
		s.addf("defaultVariant", "%q is not a valid variant id", cmd.DefaultVariant)
	}
}

// Spec is a command after a variant was applied, before placeholders are
// expanded and before it is wrapped for a shell. With Argv, PrependArgs and
// AppendArgs are already merged in; with Line they are kept apart so the
// runner can quote them for the item's shell.
type Spec struct {
	Line        string            `json:"line,omitempty"`
	Argv        []string          `json:"argv,omitempty"`
	PrependArgs []string          `json:"prependArgs,omitempty"`
	AppendArgs  []string          `json:"appendArgs,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// Apply returns base with v applied: Line or Argv replace the command,
// PrependArgs go right after the executable, AppendArgs at the end, Env is
// merged last (variant wins). base is not modified.
func (v Variant) Apply(base Spec) Spec {
	out := Spec{
		Line:        base.Line,
		Argv:        slices.Clone(base.Argv),
		PrependArgs: slices.Clone(base.PrependArgs),
		AppendArgs:  slices.Clone(base.AppendArgs),
		Env:         maps.Clone(base.Env),
	}
	switch {
	case v.Argv != nil:
		out.Line, out.Argv = "", slices.Clone(v.Argv)
	case v.Line != "":
		out.Line, out.Argv = v.Line, nil
	}
	if out.Argv != nil {
		if len(v.PrependArgs) > 0 && len(out.Argv) > 0 {
			out.Argv = slices.Concat(out.Argv[:1], v.PrependArgs, out.Argv[1:])
		}
		out.Argv = append(out.Argv, v.AppendArgs...)
	} else {
		out.PrependArgs = append(out.PrependArgs, v.PrependArgs...)
		out.AppendArgs = append(out.AppendArgs, v.AppendArgs...)
	}
	if len(v.Env) > 0 {
		if out.Env == nil {
			out.Env = map[string]string{}
		}
		maps.Copy(out.Env, v.Env)
	}
	return out
}
