// Package server is OrchDock's local service: the HTTP API, live events and
// the embedded UI. It binds to the loopback interface only and, because the API
// can start commands, every API route requires a token.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/config"
	"github.com/mohamed-foly/orchdock/internal/events"
	"github.com/mohamed-foly/orchdock/internal/fsutil"
	"github.com/mohamed-foly/orchdock/internal/runner"
	"github.com/mohamed-foly/orchdock/internal/scheduler"
)

// InfoFile is written to the home while the service runs.
const InfoFile = "server.json"

// TokenHeader carries the API token.
const TokenHeader = "X-OrchDock-Token"

// maxBody is the largest request body accepted.
const maxBody = 1 << 20

// Info is the content of server.json: how to reach a running service.
type Info struct {
	Port      int       `json:"port"`
	PID       int       `json:"pid"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"startedAt"`
	Version   string    `json:"version"`
}

// Shell is what only a native shell can do. A service without one answers 501
// for these routes.
type Shell interface {
	PickFolder(ctx context.Context, title string) (string, error)
	PickFile(ctx context.Context, title string) (string, error)
	Reveal(path string) error
	OpenURL(url string) error
}

// Server serves the API and the UI.
type Server struct {
	Home    string
	Version string
	// Port is the port to listen on. Zero means the configured port, falling
	// back to a free one if it is taken. After Listen it is the real port.
	Port   int
	Token  string
	Runner *runner.Runner
	Store  *catalog.Store
	Hub    *events.Hub
	Shell  Shell                      // nil: native routes answer 501
	OpenUI func(itemRef string) error // shows the window; nil: 501
	Quit   func()                     // stops the service; nil: 501
	// CLIExe is the console OrchDock program, used to build "attach" commands
	// for the user's terminal. OpenTerminal opens argv in a terminal window;
	// nil uses the system's.
	CLIExe       string
	OpenTerminal func(title string, argv []string) error
	// AutostartExe is the program started at sign-in ("" disables the feature).
	AutostartExe string

	mu        sync.Mutex
	attention map[string]string // item id -> reason
	monitors  map[string]bool   // run ids with a monitor connection
	prev      map[string]string // watcher: item id -> state and run
	polled    bool              // the watcher has taken its first snapshot
	kick      chan struct{}     // wakes the watcher (a listener arrived)
	started   time.Time
	svc       serviceWatch // health checks and restart policies
	sched     *scheduler.Scheduler
}

// New returns a Server with a fresh token.
func New(home, version string, rn *runner.Runner, store *catalog.Store) *Server {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return &Server{
		Home: home, Version: version, Token: hex.EncodeToString(b), Runner: rn, Store: store,
		Hub: events.NewHub(), attention: map[string]string{}, monitors: map[string]bool{}, prev: map[string]string{}, kick: make(chan struct{}, 1),
	}
}

// ErrRunning means another service already serves this home.
var ErrRunning = errors.New("the OrchDock service is already running")

// ReadInfo loads server.json and checks that its service answers.
func ReadInfo(home string) (Info, bool) {
	b, err := os.ReadFile(filepath.Join(home, InfoFile))
	if err != nil {
		return Info{}, false
	}
	var in Info
	if json.Unmarshal(b, &in) != nil || in.Port == 0 {
		return Info{}, false
	}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/ping", in.Port), nil)
	req.Header.Set(TokenHeader, in.Token)
	resp, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		return in, false
	}
	_ = resp.Body.Close()
	return in, resp.StatusCode == http.StatusOK
}

// Listen claims the home for this service and opens the port. It fails with
// ErrRunning when a service already answers for this home.
func (s *Server) Listen() (net.Listener, error) {
	if in, alive := ReadInfo(s.Home); alive {
		return nil, fmt.Errorf("%w (port %d, pid %d)", ErrRunning, in.Port, in.PID)
	}
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return nil, err
	}
	want, explicit := s.Port, s.Port != 0
	if !explicit {
		if cfg, err := config.Load(s.Home); err == nil {
			want = cfg.UIPort
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(want)))
	if err != nil && !explicit {
		ln, err = net.Listen("tcp", "127.0.0.1:0") // the configured port is taken
	}
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", want, err)
	}
	s.Port = ln.Addr().(*net.TCPAddr).Port
	s.started = time.Now()
	b, _ := json.MarshalIndent(Info{Port: s.Port, PID: os.Getpid(), Token: s.Token, StartedAt: s.started.UTC(), Version: s.Version}, "", "  ")
	if err := fsutil.WriteFileAtomic(filepath.Join(s.Home, InfoFile), b, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// Serve serves on ln until ctx ends, then removes server.json.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	defer os.Remove(filepath.Join(s.Home, InfoFile))
	s.Runner.ReconcileAll() // history of runs that ended while nothing watched
	_ = s.purgeTrash(false) // items deleted more than 30 days ago
	go s.watch(ctx)
	s.startScheduler(ctx)
	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sh)
	}()
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ListenAndServe is Listen followed by Serve.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Handler returns the routed, secured handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return s.secure(mux)
}

// secure enforces the Host allow-list, the token on every API route and JSON
// bodies, and adds the headers every response carries.
func (s *Server) secure(next http.Handler) http.Handler {
	allowed := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", s.Port): true,
		fmt.Sprintf("localhost:%d", s.Port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[strings.ToLower(r.Host)] { // blocks DNS rebinding
			writeErr(w, http.StatusMisdirectedRequest, "invalid", "host not allowed")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.tokenOK(r) {
				writeErr(w, http.StatusForbidden, "forbidden", "missing or invalid token")
				return
			}
			mutating := r.Method != http.MethodGet && r.Method != http.MethodHead
			if mutating && r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeErr(w, http.StatusUnsupportedMediaType, "invalid", "a JSON body is required")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOK checks the token in constant time. Browsers cannot set headers on
// event streams and WebSockets, so those routes (and only those) accept it in
// the query string.
func (s *Server) tokenOK(r *http.Request) bool {
	t := r.Header.Get(TokenHeader)
	if t == "" && (r.URL.Path == "/api/events" || strings.HasSuffix(r.URL.Path, "/attach")) {
		t = r.URL.Query().Get("t")
	}
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.Token)) == 1
}
