// Command orchdock is the OrchDock command line (console subsystem).
package main

import (
	"os"

	"github.com/mohamed-foly/orchdock/internal/cli"
	"github.com/mohamed-foly/orchdock/internal/paths"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	paths.LoadDotEnv() // ORCHDOCK_* settings from a .env next to the program
	cli.Version = version
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
