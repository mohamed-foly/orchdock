package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/mohamed-foly/orchdock/internal/server"
	"github.com/mohamed-foly/orchdock/internal/shell/browser"
)

func init() {
	register("serve", "[--port n]", "run the OrchDock service in this terminal (API and window page)", cmdServe)
	register("quit", "", "stop the OrchDock service (runs keep going)", cmdQuit)
}

// consoleBinary is the console program that "attach" runs in: next to
// orchdockw there is an orchdock, otherwise the running binary itself.
func consoleBinary() string {
	self, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	dir, base := filepath.Split(self)
	ext := filepath.Ext(base)
	if base[:len(base)-len(ext)] == "orchdockw" {
		if c := filepath.Join(dir, "orchdock"+ext); fileExists(c) {
			return c
		}
	}
	return self
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// NewServer builds the service for this home. The window shell uses it too.
func NewServer(e *Env) *server.Server {
	srv := server.New(e.Home, Version, e.Runner, e.Store)
	srv.CLIExe = consoleBinary()
	srv.Shell = browser.System{} // the window shell replaces it with native dialogs
	srv.AutostartExe = windowlessBinary()
	return srv
}

func cmdServe(e *Env, args []string) error {
	fs := newFlags("serve", e.Err)
	port := fs.Int("port", 0, "port to listen on (default: the configured one)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 0, 0, "serve [--port n]"); err != nil {
		return err
	}
	srv := NewServer(e)
	srv.Port = *port
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv.Quit = cancel
	ln, err := srv.Listen()
	if err != nil {
		if errors.Is(err, server.ErrRunning) {
			return fmt.Errorf("%w: %v", errConflict, err)
		}
		return err
	}
	fmt.Fprintf(e.Out, "OrchDock is running at http://127.0.0.1:%d/ (Ctrl+C or 'orchdock quit' stops it; runs keep going)\n", srv.Port)
	return srv.Serve(ctx, ln)
}

func cmdQuit(e *Env, _ []string) error {
	info, alive := server.ReadInfo(e.Home)
	if !alive {
		fmt.Fprintln(e.Out, "the service is not running")
		return nil
	}
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/quit", info.Port), nil)
	req.Header.Set(server.TokenHeader, info.Token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the service refused to quit: %s", resp.Status)
	}
	fmt.Fprintln(e.Out, "stopped the service (runs keep going)")
	return nil
}

// windowlessBinary is the program started at sign-in: orchdockw next to the
// console program, or the running binary itself.
func windowlessBinary() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	dir, base := filepath.Split(self)
	ext := filepath.Ext(base)
	if name := base[:len(base)-len(ext)]; name == "orchdock" || name == "odock" {
		if w := filepath.Join(dir, "orchdockw"+ext); fileExists(w) {
			return w
		}
	}
	return self
}
