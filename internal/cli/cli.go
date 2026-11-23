// Package cli implements the orchdock command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/detach"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/paths"
	"github.com/mohamed-foly/orchdock/internal/runner"
)

// Version is injected by main.
var Version = "dev"

// Stdin is the input commands read from (attach, send). Tests replace it.
var Stdin io.Reader = os.Stdin

// Exit codes.
const (
	ExitOK       = 0
	ExitUsage    = 1 // usage error or ambiguous reference
	ExitNotFound = 2
	ExitConflict = 3 // already running, port busy
	ExitStart    = 4 // failed to start
)

// ExitError makes a command end with a specific exit code. "run --wait" uses
// it to pass on the exit code of the command it ran.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// exitCode maps an error to the documented exit codes.
func exitCode(err error) int {
	var ee *ExitError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &ee):
		return ee.Code
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, host.ErrNoHost):
		return ExitNotFound
	case errors.Is(err, domain.ErrConflict):
		return ExitConflict
	case errors.Is(err, domain.ErrStartFailed):
		return ExitStart
	default:
		return ExitUsage
	}
}

// Env carries what a command needs.
type Env struct {
	Home   string
	Out    io.Writer
	Err    io.Writer
	Store  *catalog.Store
	Runner *runner.Runner
}

type command struct {
	summary string
	usage   string
	hidden  bool
	run     func(e *Env, args []string) error
}

var commands = map[string]command{}

// register adds a command. usage is the argument synopsis shown in the help.
func register(name, usage, summary string, run func(e *Env, args []string) error) {
	commands[name] = command{summary: summary, usage: usage, run: run}
}

// registerHidden adds a command that is not listed in the help.
func registerHidden(name string, run func(e *Env, args []string) error) {
	commands[name] = command{hidden: true, run: run}
}

// errConflict marks "already running" style failures (exit code 3).
var errConflict = domain.ErrConflict

// ErrUnavailable marks a command that this build does not provide yet.
var ErrUnavailable = errors.New("not available in this build yet")

func init() {
	register("version", "", "print the version", func(e *Env, _ []string) error {
		fmt.Fprintln(e.Out, "orchdock", Version)
		return nil
	})
	register("home", "", "print the data directory", func(e *Env, _ []string) error {
		fmt.Fprintln(e.Out, e.Home)
		return nil
	})
	// "orchdock host <runDir>" is how a detached host process is started.
	registerHidden("host", func(_ *Env, args []string) error {
		if len(args) != 1 {
			return errors.New("usage: orchdock host <runDir>")
		}
		return host.Run(args[0])
	})
}

// Run parses global flags, resolves the home and dispatches to a subcommand.
func Run(args []string, out, errOut io.Writer) int {
	global := flag.NewFlagSet("orchdock", flag.ContinueOnError)
	global.SetOutput(errOut)
	homeFlag := global.String("home", "", "data directory (overrides "+paths.EnvHome+")")
	global.Usage = func() { usage(errOut) }
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	rest := global.Args()
	if len(rest) == 0 {
		rest = []string{"ui"} // double-click or bare command opens the window
	}
	name := rest[0]
	if name == "help" {
		usage(out)
		return ExitOK
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(errOut, "unknown command %q\n\n", name)
		usage(errOut)
		return ExitUsage
	}
	home, err := paths.Home(*homeFlag)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return ExitUsage
	}
	store := catalog.New(home, nil)
	exe, kind := hostBinary()
	rn := runner.New(home, store, exe)
	rn.HostKind = kind
	env := &Env{Home: home, Out: out, Err: errOut, Store: store, Runner: rn}
	if err := cmd.run(env, rest[1:]); err != nil {
		var ee *ExitError
		if errors.As(err, &ee) && ee.Msg == "" {
			return ee.Code // the command reported everything itself
		}
		fmt.Fprintln(errOut, "error:", err)
		return exitCode(err)
	}
	return ExitOK
}

// hostBinary picks the program that runs hosts. Next to orchdock there may be
// orchdockw, the windowless build: using it means no console window ever
// flashes when a run starts. Otherwise the running binary hosts itself.
func hostBinary() (string, detach.Kind) {
	self, err := os.Executable()
	if err != nil {
		return os.Args[0], detach.Console
	}
	dir, base := filepath.Split(self)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	if name == "orchdock" {
		w := filepath.Join(dir, "orchdockw"+ext)
		if _, err := os.Stat(w); err == nil {
			return w, detach.GUI
		}
	}
	if name == "orchdockw" {
		return self, detach.GUI
	}
	return self, detach.Console
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: orchdock [--home DIR] <command> [args]")
	fmt.Fprintln(w)
	names := make([]string, 0, len(commands))
	for n, c := range commands {
		if !c.hidden {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		c := commands[n]
		head := n
		if c.usage != "" {
			head += " " + c.usage
		}
		fmt.Fprintf(w, "  %-34s %s\n", head, c.summary)
	}
	fmt.Fprintln(w, "\n<ref> is an alias, an id, or a unique id prefix (4+ characters).")
	fmt.Fprintln(w, "Exit codes: 0 ok, 1 usage or ambiguous, 2 not found, 3 conflict, 4 failed to start.")
}
