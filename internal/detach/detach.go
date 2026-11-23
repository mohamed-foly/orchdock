// Package detach starts processes that outlive OrchDock.
package detach

// Kind says which subsystem the program to start uses (matters on Windows only).
type Kind int

const (
	// Console is a console-subsystem program (shells, the CLI, most tools).
	// On Windows it gets a hidden console of its own (CREATE_NO_WINDOW), which
	// its console children share, so no window flashes.
	Console Kind = iota
	// GUI is a GUI-subsystem program (for example the windowless launcher that
	// also runs hosts). On Windows it starts without any console
	// (DETACHED_PROCESS). HideWindow is never set: it would hide the program's
	// first window.
	GUI
)
