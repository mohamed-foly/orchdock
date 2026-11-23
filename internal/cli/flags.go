package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// stringList is a repeatable flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// newFlags returns a flag set whose errors are returned, not printed twice.
func newFlags(name string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	return fs
}

// parse parses args with flags and positional arguments in any order, so that
// "orchdock add task Title --cmd x" and "orchdock add --cmd x task Title" both
// work. It returns the positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
		// "--" ends the flags: everything after it is positional.
		if len(pos) > 0 && len(args) > 0 && args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
	}
}

// isSet reports which flags were given on the command line.
func isSet(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// need checks the number of positional arguments.
func need(pos []string, min, max int, usage string) error {
	if len(pos) < min || (max >= 0 && len(pos) > max) {
		return fmt.Errorf("usage: orchdock %s", usage)
	}
	return nil
}

// splitList splits "a,b,c" into its parts, ignoring empty ones.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseEnv turns KEY=VALUE arguments into a map.
func parseEnv(list []string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--env %q: expected KEY=VALUE", kv)
		}
		out[k] = v
	}
	return out, nil
}
