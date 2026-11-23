//go:build server || (!windows && !cgo)

// Package wails is the native shell; this build has none.
package wails

import (
	"context"
	"errors"

	"github.com/mohamed-foly/orchdock/internal/events"
	"github.com/mohamed-foly/orchdock/internal/shell/browser"
)

// Available reports whether this build has the native shell.
const Available = false

// Entry is one running item as the tray menu shows it.
type Entry struct{ ID, Label string }

// Options configure the shell.
type Options struct {
	Home    string
	URL     func() string
	Running func() []Entry
	Stop    func(id string)
	Quit    func()
	Hub     *events.Hub
	Tray    bool
	// Attention counts the items that want the user.
	Attention func() int
}

// Shell is absent in this build.
type Shell struct{ browser.System }

// New returns an empty shell.
func New(Options) *Shell { return &Shell{} }

// Run reports that there is no native shell.
func (*Shell) Run(context.Context) error { return errors.New("this build has no native window") }

// Open reports that there is no native shell.
func (*Shell) Open(string) error { return errors.New("this build has no native window") }
