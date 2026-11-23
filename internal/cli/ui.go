package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/events"
	"github.com/mohamed-foly/orchdock/internal/runner"
	"github.com/mohamed-foly/orchdock/internal/server"
	"github.com/mohamed-foly/orchdock/internal/shell/browser"
	"github.com/mohamed-foly/orchdock/internal/shell/wails"
)

func init() {
	register("ui", "[--tray]", "open the window (starts the service and the tray icon if needed)", cmdUI)
}

func cmdUI(e *Env, args []string) error {
	fs := newFlags("ui", e.Err)
	tray := fs.Bool("tray", false, "start with the tray icon only, no window")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 0, 0, "ui [--tray]"); err != nil {
		return err
	}
	if info, alive := server.ReadInfo(e.Home); alive { // the app is already running: bring it forward
		if *tray {
			return nil
		}
		return askToOpen(info)
	}
	srv := NewServer(e)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv.Quit = cancel
	ln, err := srv.Listen()
	if err != nil {
		if errors.Is(err, server.ErrRunning) { // lost a race with another start
			if info, alive := server.ReadInfo(e.Home); alive && !*tray {
				return askToOpen(info)
			}
			return nil
		}
		return err
	}
	url := func() string { return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", srv.Port, srv.Token) }
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln) }()

	if !wails.Available { // no native window in this build: use the default browser
		srv.Shell = browser.System{}
		srv.OpenUI = func(ref string) error {
			if ref != "" {
				return browser.Open(url() + "#item=" + ref)
			}
			return browser.Open(url())
		}
		if !*tray {
			_ = browser.Open(url())
		}
		fmt.Fprintf(e.Out, "OrchDock is running at http://127.0.0.1:%d/ (Ctrl+C stops it; runs keep going)\n", srv.Port)
		return <-served
	}
	sh := wails.New(wails.Options{
		Home: e.Home, URL: url, Hub: srv.Hub, Tray: *tray, Quit: cancel, Attention: srv.AttentionCount,
		Running: func() []wails.Entry { return runningEntries(e) },
		Stop: func(id string) {
			_, _ = e.Runner.Stop(id, runner.StopOptions{})
			srv.Hub.Publish(events.Event{Type: "item.changed"})
		},
	})
	srv.Shell = sh
	srv.OpenUI = sh.Open
	if err := sh.Run(ctx); err != nil {
		cancel()
		return err
	}
	cancel()
	select {
	case serr := <-served:
		if serr != nil && !errors.Is(serr, context.Canceled) {
			return serr
		}
	case <-time.After(3 * time.Second):
	}
	return nil
}

// askToOpen tells the running service to show its window.
func askToOpen(info server.Info) error {
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/ui/open", info.Port), bytes.NewReader([]byte("{}")))
	req.Header.Set(server.TokenHeader, info.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the running service could not open its window: %s", resp.Status)
	}
	return nil
}

// runningEntries lists the items that have a live run, by title.
func runningEntries(e *Env) []wails.Entry {
	cat, err := e.Store.Load()
	if err != nil {
		return nil
	}
	var out []wails.Entry
	for id, st := range e.Runner.Statuses(cat.Items) {
		if st.State != runner.StateRunning && st.State != runner.StateHeld {
			continue
		}
		out = append(out, wails.Entry{ID: id, Label: label(cat.Items, id)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func label(items []domain.Item, id string) string {
	for _, it := range items {
		if it.ID == id {
			return it.Alias // titles repeat ("Backlog board" in every project); aliases are unique
		}
	}
	return id
}
