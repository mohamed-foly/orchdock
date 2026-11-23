// Package command turns an item and a chosen variant into exactly what gets
// executed, and into the text the Copy button puts on the clipboard. Both come
// from one function so that what you copy is what OrchDock runs.
//
// Everything that runs is just a command in a terminal: nothing here depends
// on the kind of item (agent, service, task or shell). A caller that has a
// different source of commands, such as an agent profile, builds an Input.
//
// The package starts no processes. It reads only the environment file an item
// names, through an injectable reader.
package command

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// Input is everything needed to resolve one command.
type Input struct {
	Shell       string            // a domain shell; empty or "auto" picks the platform default
	Base        domain.Spec       // the command before any variant: Line or Argv
	Cwd         string            // may use placeholders; empty means the project folder, else the home
	Env         map[string]string // explicit environment (placeholders allowed)
	EnvFile     string            // optional .env file, relative to the working directory
	SecretEnv   []string          // names in Env that Copy must not reveal
	LoadProfile bool              // let the shell load the user's profile

	Variants       []domain.Variant // already merged (profile + item)
	ItemDefault    string
	ProfileDefault string

	Vars map[string]string // placeholder values: port, sessionId, projectDir, ...
}

// FromItem builds the Input for an item that carries its own command.
func FromItem(item domain.Item, project *domain.Project, vars map[string]string) Input {
	v := map[string]string{"itemId": item.ID, "alias": item.Alias}
	if project != nil {
		v["projectDir"] = project.Path
		v["projectName"] = project.Name
	}
	for k, val := range vars {
		v[k] = val
	}
	c := item.Command
	return Input{
		Shell:       c.Shell,
		Base:        domain.Spec{Line: c.Line, Argv: c.Argv, Env: c.Env},
		Cwd:         c.Cwd,
		Env:         c.Env,
		EnvFile:     c.EnvFile,
		SecretEnv:   c.SecretEnv,
		LoadProfile: c.LoadProfile,
		Variants:    domain.MergeVariants(nil, c.Variants),
		ItemDefault: c.DefaultVariant,
		Vars:        v,
	}
}

// Env describes the machine the command will run on. Zero values mean "this
// machine"; tests replace the functions.
type Env struct {
	OS       string                       // runtime.GOOS
	Environ  []string                     // the environment the command inherits
	Home     string                       // the user's home folder
	LookPath func(string) (string, error) // finds executables on PATH
	ReadFile func(string) ([]byte, error) // reads the env file
}

// Resolved is a command ready to hand to a host.
type Resolved struct {
	Argv    []string // what the host executes
	Cwd     string
	Env     []string // full environment, KEY=VALUE
	Shell   string   // pwsh, powershell, cmd, bash, zsh, sh, or "none" for a direct command
	Variant string   // id of the variant that was applied

	text    string            // the command as typed in the user's shell
	set     map[string]string // explicit environment (item + variant), for Copy
	secret  map[string]bool
	dialect dialect
}

var placeholderRe = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)

// Resolve applies the variant (chosen, else the item's default, else the
// profile's default) and returns the command to run.
func Resolve(in Input, variantID string, e Env) (Resolved, error) {
	e = e.withDefaults()
	v, err := domain.ResolveVariant(in.Variants, variantID, in.ItemDefault, in.ProfileDefault)
	if err != nil {
		return Resolved{}, err
	}
	base := in.Base
	base.Env = in.Env
	spec := v.Apply(base)

	vars := map[string]string{"home": e.Home}
	for k, val := range in.Vars {
		vars[k] = val
	}
	x := expander{vars: vars}
	line := x.one("command", spec.Line)
	argv := x.all("command", spec.Argv)
	prepend := x.all("prependArgs", spec.PrependArgs)
	appendA := x.all("appendArgs", spec.AppendArgs)
	setEnv := map[string]string{}
	for k, val := range spec.Env {
		setEnv[k] = x.one("env."+k, val)
	}
	cwd := x.one("cwd", in.Cwd)
	envFile := x.one("envFile", in.EnvFile)
	if x.err != nil {
		return Resolved{}, x.err
	}
	if cwd == "" {
		cwd = vars["projectDir"]
	}
	if cwd == "" {
		cwd = e.Home
	}

	environ, err := buildEnv(e, cwd, envFile, setEnv)
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{Cwd: cwd, Env: environ, Variant: v.ID, set: setEnv, secret: map[string]bool{}}
	for _, n := range in.SecretEnv {
		r.secret[n] = true
	}

	switch {
	case len(argv) > 0:
		return r.direct(e, argv)
	default:
		return r.viaShell(e, in, line, prepend, appendA)
	}
}

// direct runs argv without a shell. Windows script shims are wrapped.
func (r Resolved) direct(e Env, argv []string) (Resolved, error) {
	exe, err := e.LookPath(argv[0])
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: cannot find %q on PATH", domain.ErrNotFound, argv[0])
	}
	shell, err := pickShell(e, "auto")
	if err != nil {
		return Resolved{}, err
	}
	r.Shell = domain.ShellNone
	r.dialect = shell.dialect
	r.text = shell.dialect.join(argv)
	r.Argv = e.wrapShim(exe, argv[1:])
	return r, nil
}

// viaShell runs a command line through a shell, or an interactive shell when
// there is no line.
func (r Resolved) viaShell(e Env, in Input, line string, prepend, appendA []string) (Resolved, error) {
	shell, err := pickShell(e, in.Shell)
	if err != nil {
		return Resolved{}, err
	}
	r.Shell = shell.name
	r.dialect = shell.dialect
	if line != "" {
		line = insertAfterFirstWord(line, shell.dialect.joinArgs(prepend))
		if len(appendA) > 0 {
			line += " " + shell.dialect.joinArgs(appendA)
		}
		r.text = line
	}
	r.Argv = shell.argv(line, in.LoadProfile)
	return r, nil
}

// joinArgs quotes arguments that follow an executable.
func (d dialect) joinArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = d.quote(a)
	}
	return strings.Join(out, " ")
}

// expander expands placeholders and records the first that has no value.
type expander struct {
	vars map[string]string
	err  error
}

func (x *expander) one(field, s string) string {
	out := domain.Expand(s, x.vars)
	for _, m := range placeholderRe.FindAllStringSubmatch(out, -1) {
		if slices.Contains(domain.Placeholders, m[1]) && x.err == nil {
			x.err = fmt.Errorf("%w: %s uses {%s} but it has no value here", domain.ErrInvalid, field, m[1])
		}
	}
	return out
}

func (x *expander) all(field string, list []string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = x.one(field, s)
	}
	return out
}

// buildEnv layers the inherited environment, the env file and the explicit
// environment (later layers win). On Windows names compare case-insensitively.
func buildEnv(e Env, cwd, envFile string, explicit map[string]string) ([]string, error) {
	out := append([]string(nil), e.Environ...)
	set := func(k, val string) {
		for i, kv := range out {
			name, _, _ := strings.Cut(kv, "=")
			if name == k || (e.OS == "windows" && strings.EqualFold(name, k)) {
				out[i] = k + "=" + val
				return
			}
		}
		out = append(out, k+"="+val)
	}
	if envFile != "" {
		path := envFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		data, err := e.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("env file: %w", err)
		}
		vals, err := parseEnvFile(data)
		if err != nil {
			return nil, fmt.Errorf("%w: env file %s: %v", domain.ErrInvalid, envFile, err)
		}
		for _, k := range sortedKeys(vals) {
			set(k, vals[k])
		}
	}
	for _, k := range sortedKeys(explicit) {
		set(k, explicit[k])
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (e Env) withDefaults() Env {
	if e.OS == "" {
		e.OS = goos()
	}
	if e.Environ == nil {
		e.Environ = os.Environ()
	}
	if e.Home == "" {
		e.Home, _ = os.UserHomeDir()
	}
	if e.LookPath == nil {
		e.LookPath = lookPath
	}
	if e.ReadFile == nil {
		e.ReadFile = os.ReadFile
	}
	return e
}
