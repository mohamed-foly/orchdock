// Package proctree controls the whole process tree of a run, not just the
// process the host started. npm, pnpm and shells spawn grandchildren, and
// stopping a run must stop them too.
//
// On Windows a Job Object with kill-on-close holds the tree; elsewhere the
// child leads a process group that is signalled as a whole.
package proctree

// Tree is a set of processes that is stopped together.
type Tree interface {
	// Terminate asks every process to end where the platform can (SIGTERM on
	// Unix). On Windows it ends them at once, as there is no gentler call.
	Terminate() error
	// Kill ends every process immediately.
	Kill() error
	// Close releases the tree. Processes still in it are killed so that a
	// finished run never leaves orphans behind.
	Close() error
}
