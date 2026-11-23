//go:build !server && (windows || cgo)

// Package wails is the native shell: window, tray, dialogs, single instance.
package wails

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	wevents "github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/mohamed-foly/orchdock/internal/events"
	"github.com/mohamed-foly/orchdock/internal/shell"
	"github.com/mohamed-foly/orchdock/internal/shell/browser"
)

// Available reports whether this build has the native shell.
const Available = true

// Entry is one running item as the tray menu shows it.
type Entry struct {
	ID    string
	Label string
}

// Options configure the shell.
type Options struct {
	Home    string
	URL     func() string // window address, known once the service listens
	Running func() []Entry
	Stop    func(id string)
	Quit    func()      // stops the service; runs keep going
	Hub     *events.Hub // tray refresh trigger
	Tray    bool        // start with the tray only, no window
	// Attention counts the items that want the user (an amber dot on the tray).
	Attention func() int
}

// Shell owns the Wails app.
type Shell struct {
	o    Options
	app  *application.App
	tray *application.SystemTray
	mu   sync.Mutex
	win  *application.WebviewWindow
	note *notifications.NotificationService
	// opening guards Open against re-entry: WebView2 start-up pumps messages,
	// so a second tray click runs inside the first Open.
	opening atomic.Bool
}

// New creates the Wails app; call Run on the main goroutine.
func New(o Options) *Shell {
	s := &Shell{o: o, note: notifications.New()}
	sum := sha256.Sum256([]byte(filepath.Clean(o.Home)))
	s.app = application.New(application.Options{
		Name:        "OrchDock",
		Services:    []application.Service{application.NewService(s.note)},
		Description: "Run and keep track of your terminals, services and agents",
		Icon:        shell.PNG(256, shell.BadgeNone),
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
			WebviewUserDataPath:           filepath.Join(o.Home, "ui-webview"),
		},
		Linux: application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
		Mac:   application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "io.orchdock." + hex.EncodeToString(sum[:6]),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { _ = s.Open("") },
		},
	})
	// clicking a notification opens the window on its item
	s.note.OnNotificationResponse(func(r notifications.NotificationResult) {
		id, _ := r.Response.UserInfo["itemId"].(string)
		_ = s.Open(id)
	})
	return s
}

// Run starts the tray and window and blocks until quit.
func (s *Shell) Run(ctx context.Context) error {
	s.tray = s.app.SystemTray.New()
	s.tray.SetIcon(shell.TrayIcon(false, false))
	s.tray.SetTooltip("OrchDock")
	s.tray.OnClick(func() { _ = s.Open("") })
	s.rebuildMenu()
	go s.follow(ctx)
	go s.watchdog(ctx)
	go func() {
		<-ctx.Done()
		s.app.Quit()
	}()
	if !s.o.Tray {
		s.mu.Lock()
		s.showWindow("") // before Run: no main-thread dispatch yet
		s.mu.Unlock()
	}
	err := s.app.Run()
	if s.o.Quit != nil {
		s.o.Quit()
	}
	return err
}

// watchdog gives up waiting politely when the UI thread stops answering: it
// only reports, since a stuck thread cannot be repaired from here.
func (s *Shell) watchdog(ctx context.Context) {
	const every, limit = 10 * time.Second, 20 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		done := make(chan struct{})
		go application.InvokeSync(func() { close(done) })
		select {
		case <-done:
		case <-ctx.Done():
			return
		case <-time.After(limit):
			select {
			case <-done:
			case <-ctx.Done():
				return
			}
		}
	}
}

// Open shows the window, creating it if needed; with an item ref it opens
// that item's details.
func (s *Shell) Open(itemRef string) error {
	application.InvokeSync(func() {
		if !s.opening.CompareAndSwap(false, true) {
			return // already opening on this thread
		}
		defer s.opening.Store(false)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.showWindow(itemRef)
	})
	return nil
}

// showWindow focuses or creates the window, on an item when ref is set; the
// caller holds mu.
func (s *Shell) showWindow(ref string) {
	hash := ""
	if ref != "" {
		hash = "#item=" + url.PathEscape(ref)
	}
	if s.win != nil {
		s.win.Show()
		s.win.Restore()
		s.win.Focus()
		if hash != "" {
			s.win.ExecJS(fmt.Sprintf("location.hash = %q", hash))
		}
		return
	}
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "OrchDock", Width: 1100, Height: 760, MinWidth: 360, MinHeight: 480,
		URL: s.o.URL() + hash,
	})
	w.OnWindowEvent(wevents.Common.WindowClosing, func(*application.WindowEvent) {
		s.mu.Lock()
		s.win = nil
		s.mu.Unlock()
		time.AfterFunc(3*time.Second, debug.FreeOSMemory) // give the window's memory back
	})
	s.win = w
}

// PickFolder implements server.Shell.
func (s *Shell) PickFolder(_ context.Context, title string) (string, error) {
	if title == "" {
		title = "Choose a folder"
	}
	return s.app.Dialog.OpenFile().SetTitle(title).CanChooseDirectories(true).CanChooseFiles(false).PromptForSingleSelection()
}

// PickFile implements server.Shell.
func (s *Shell) PickFile(_ context.Context, title string) (string, error) {
	if title == "" {
		title = "Choose a file"
	}
	return s.app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true).PromptForSingleSelection()
}

// Reveal implements server.Shell.
func (s *Shell) Reveal(path string) error { return browser.Reveal(path) }

// OpenURL implements server.Shell.
func (s *Shell) OpenURL(url string) error { return browser.Open(url) }

// follow refreshes the tray a moment after anything changes.
func (s *Shell) follow(ctx context.Context) {
	if s.o.Hub == nil {
		return
	}
	ch, cancel := s.o.Hub.Subscribe()
	defer cancel()
	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Type == "attention" {
				s.notify(e.Data)
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(300*time.Millisecond, s.rebuildMenu)
		}
	}
}

func (s *Shell) rebuildMenu() {
	var running []Entry
	if s.o.Running != nil {
		running = s.o.Running()
	}
	menu := s.app.NewMenu()
	for _, e := range running {
		e := e
		menu.Add("●  Stop " + e.Label).OnClick(func(*application.Context) {
			if s.o.Stop != nil {
				go s.o.Stop(e.ID)
			}
		})
	}
	if len(running) > 0 {
		menu.AddSeparator()
	}
	menu.Add("Open OrchDock").OnClick(func(*application.Context) { _ = s.Open("") })
	menu.AddSeparator()
	menu.Add("Quit (runs keep going)").OnClick(func(*application.Context) { s.app.Quit() })
	s.tray.SetMenu(menu)
	waiting := 0
	if s.o.Attention != nil {
		waiting = s.o.Attention()
	}
	s.tray.SetIcon(shell.TrayIcon(len(running) > 0, waiting > 0))
	tip := "OrchDock"
	if len(running) > 0 {
		tip = fmt.Sprintf("OrchDock — %d running", len(running))
	}
	if waiting > 0 {
		tip += fmt.Sprintf(" · %d need you", waiting)
	}
	s.tray.SetTooltip(tip)
}

// notify shows a native notification for an attention event.
func (s *Shell) notify(data any) {
	m, _ := data.(map[string]any)
	id, _ := m["itemId"].(string)
	alias, _ := m["alias"].(string)
	reason, _ := m["reason"].(string)
	if id == "" {
		return
	}
	_ = s.note.SendNotification(notifications.NotificationOptions{
		ID:    fmt.Sprintf("orchdock-%s-%d", id, time.Now().UnixNano()),
		Title: alias + " needs you",
		Body:  reason,
		Data:  map[string]any{"itemId": id},
	})
}
