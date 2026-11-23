// Package browser opens URLs and folders with the system's defaults.
package browser

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Open shows url in the default browser.
func Open(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// Reveal shows path in the file manager.
func Reveal(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,", filepath.Clean(path)).Start()
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}

// System is a server shell that has no native dialogs: folders and files
// cannot be picked, but URLs and folders can be opened.
type System struct{}

// PickFolder is not available without a native shell.
func (System) PickFolder(context.Context, string) (string, error) { return "", ErrNoDialog }

// PickFile is not available without a native shell.
func (System) PickFile(context.Context, string) (string, error) { return "", ErrNoDialog }

// Reveal shows path in the file manager.
func (System) Reveal(path string) error { return Reveal(path) }

// OpenURL shows url in the default browser.
func (System) OpenURL(url string) error { return Open(url) }

// ErrNoDialog means this build has no native file dialogs.
var ErrNoDialog = errors.New("this build has no file dialog; type the path instead")
