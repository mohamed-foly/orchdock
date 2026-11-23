// Command orchdockw is the windowless launcher (GUI subsystem on Windows):
// double-click opens the window, and it also runs the per-run hosts so no
// console window ever flashes. There is no console, so errors go to
// <home>/logs/orchdock.log.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/cli"
	"github.com/mohamed-foly/orchdock/internal/paths"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	paths.LoadDotEnv() // ORCHDOCK_* settings from a .env next to the program
	cli.Version = version
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"ui"}
	}
	var out, errOut bytes.Buffer
	code := cli.Run(args, &out, &errOut)
	if errOut.Len() > 0 {
		logError(homeFlag(args), errOut.String())
	}
	os.Exit(code)
}

// homeFlag returns the value of a --home flag before the command, if any.
func homeFlag(args []string) string {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return ""
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name != "home" {
			continue
		}
		if hasVal {
			return val
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// logError appends to <home>/logs/orchdock.log.
func logError(flagHome, msg string) {
	home, err := paths.Home(flagHome)
	if err != nil {
		return
	}
	dir := filepath.Join(home, "logs")
	_ = os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, "orchdock.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), msg)
}
