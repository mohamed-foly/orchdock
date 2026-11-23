//go:build windows

package attach

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVTInput makes the console deliver key presses as terminal sequences
// (arrows, function keys), which is what the program in the run expects. It
// returns a function that restores the previous mode.
func enableVTInput(f *os.File) func() {
	return setConsoleFlag(f, windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
}

// enableVTOutput makes the console interpret the terminal sequences in the
// run's output (colours, cursor movement).
func enableVTOutput(f *os.File) func() {
	return setConsoleFlag(f, windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}

func setConsoleFlag(f *os.File, flag uint32) func() {
	h := windows.Handle(f.Fd())
	var old uint32
	if err := windows.GetConsoleMode(h, &old); err != nil {
		return func() {}
	}
	if err := windows.SetConsoleMode(h, old|flag); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, old) }
}
