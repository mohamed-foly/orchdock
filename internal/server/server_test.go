package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/cli"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/runner"
	"github.com/mohamed-foly/orchdock/internal/server"
	"github.com/mohamed-foly/orchdock/internal/testchild"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

// The test binary stands in for orchdock (as a host) and for the commands the
// hosts run, chosen by environment variables.
func TestMain(m *testing.M) {
	switch os.Getenv(testchild.EnvRole) {
	case "cli":
		os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
	case "child":
		testchild.Main(os.Getenv(testchild.EnvMode), os.Getenv(testchild.EnvArg))
		os.Exit(0)
	}
	_ = os.Setenv(testchild.EnvRole, "cli")
	os.Exit(m.Run())
}

// svc is a running service on a temporary home.
type svc struct {
	t      *testing.T
	home   string
	srv    *server.Server
	base   string
	cancel context.CancelFunc
	done   chan error

	stopOnce sync.Once
}

func start(t *testing.T, home string) *svc {
	t.Helper()
	if home == "" {
		home = t.TempDir()
	}
	store := catalog.New(home, nil)
	rn := runner.New(home, store, os.Args[0])
	srv := server.New(home, "test", rn, store)
	srv.CLIExe = "orchdock-cli"
	ln, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &svc{t: t, home: home, srv: srv, base: fmt.Sprintf("http://127.0.0.1:%d", srv.Port), cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		s.stop()
		runs, _ := rn.AllRuns() // never leave a host running
		for _, r := range runs {
			if r.State == runner.StateRunning || r.State == runner.StateHeld {
				_, _ = rn.Stop(r.ItemID, runner.StopOptions{Force: true})
			}
		}
	})
	return s
}

func (s *svc) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			s.t.Error("the service did not stop")
		}
	})
}

// call sends a request with the token and decodes a JSON reply into out.
func (s *svc) call(method, path string, body any, out any) int {
	s.t.Helper()
	status, data := s.raw(method, path, body, map[string]string{server.TokenHeader: s.srv.Token})
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatalf("%s %s: bad JSON %q: %v", method, path, data, err)
		}
	}
	return status
}

func (s *svc) raw(method, path string, body any, headers map[string]string) (int, []byte) {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.base+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// testItem is the JSON of an item that runs the test binary in a mode.
func testItem(t *testing.T, kind, alias, mode, arg string) map[string]any {
	return map[string]any{
		"kind": kind, "title": alias, "alias": alias,
		"command": map[string]any{
			"shell": "none", "argv": []string{os.Args[0]}, "cwd": t.TempDir(),
			"env": map[string]string{testchild.EnvRole: "child", testchild.EnvMode: mode, testchild.EnvArg: arg},
		},
	}
}

func (s *svc) addItem(kind, alias, mode, arg string) {
	s.t.Helper()
	if code := s.call("POST", "/api/items", testItem(s.t, kind, alias, mode, arg), nil); code != 201 {
		s.t.Fatalf("add %s: status %d", alias, code)
	}
}

type itemJSON struct {
	ID     string `json:"id"`
	Alias  string `json:"alias"`
	Status struct {
		State string `json:"state"`
		RunID string `json:"runId"`
		Last  string `json:"last"`
	} `json:"status"`
}

func (s *svc) items() map[string]itemJSON {
	s.t.Helper()
	var st struct {
		Items []itemJSON `json:"items"`
	}
	if code := s.call("GET", "/api/state", nil, &st); code != 200 {
		s.t.Fatalf("state: %d", code)
	}
	m := map[string]itemJSON{}
	for _, it := range st.Items {
		m[it.Alias] = it
	}
	return m
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEveryAPIRouteNeedsTheToken(t *testing.T) {
	s := start(t, "")
	routes := []struct{ method, path string }{
		{"GET", "/api/ping"}, {"GET", "/api/state"}, {"GET", "/api/events"}, {"POST", "/api/items"},
		{"PATCH", "/api/items/x"}, {"DELETE", "/api/items/x"}, {"POST", "/api/items/x/run"}, {"POST", "/api/items/x/stop"},
		{"GET", "/api/items/x/history"}, {"GET", "/api/runs/r_x/log"}, {"POST", "/api/bulk"}, {"GET", "/api/ports"},
		{"GET", "/api/config"}, {"PUT", "/api/config"}, {"POST", "/api/shell/reveal"}, {"POST", "/api/quit"}, {"GET", "/api/nonexistent"},
	}
	for _, r := range routes {
		for name, hdr := range map[string]map[string]string{
			"no token":    nil,
			"wrong token": {server.TokenHeader: strings.Repeat("0", 64)},
			"empty token": {server.TokenHeader: ""},
		} {
			if code, _ := s.raw(r.method, r.path, map[string]any{}, hdr); code != http.StatusForbidden {
				t.Errorf("%s %s (%s): status %d, want 403", r.method, r.path, name, code)
			}
		}
	}
	// The query-string token works only for the event stream.
	if code, _ := s.raw("GET", "/api/state?t="+s.srv.Token, nil, nil); code != http.StatusForbidden {
		t.Errorf("a query token must not authorise /api/state, got %d", code)
	}
	resp, err := http.Get(s.base + "/api/events?t=" + s.srv.Token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("the event stream accepts the query token, got %d", resp.StatusCode)
	}
}

func TestHostAndContentTypeAreChecked(t *testing.T) {
	s := start(t, "")
	good := map[string]string{server.TokenHeader: s.srv.Token}
	req, _ := http.NewRequest("GET", s.base+"/api/ping", nil)
	req.Host = "evil.example"
	req.Header.Set(server.TokenHeader, s.srv.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("a foreign Host header must be 421, got %d", resp.StatusCode)
	}
	if code, _ := s.raw("GET", "/", nil, nil); code != 200 {
		t.Errorf("the page itself needs no token: %d", code)
	}
	// text/plain body on a mutating route: 415.
	req, _ = http.NewRequest("POST", s.base+"/api/bulk", strings.NewReader(`{"action":"run"}`))
	req.Header.Set(server.TokenHeader, s.srv.Token)
	req.Header.Set("Content-Type", "text/plain")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain must be 415, got %d", resp.StatusCode)
	}
	// Bad JSON is 400 and an oversized body is refused.
	req, _ = http.NewRequest("POST", s.base+"/api/items", strings.NewReader(`{not json`))
	for k, v := range good {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad JSON must be 400, got %d", resp.StatusCode)
	}
	big := `{"kind":"task","title":"` + strings.Repeat("x", 2<<20) + `"}`
	req, _ = http.NewRequest("POST", s.base+"/api/items", strings.NewReader(big))
	for k, v := range good {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an oversized body must be refused, got %d", resp.StatusCode)
	}
}

func TestPageCarriesTheTokenAndASecurityPolicy(t *testing.T) {
	s := start(t, "")
	resp, err := http.Get(s.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), s.srv.Token) || strings.Contains(string(b), "{{TOKEN}}") {
		t.Error("the page must carry the token")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestItemsRunStopHistoryAndCopyThroughTheAPI(t *testing.T) {
	s := start(t, "")
	s.addItem("shell", "echoer", "echo", "")

	var dup map[string]string
	if code := s.call("POST", "/api/items", testItem(t, "shell", "echoer", "echo", ""), &dup); code != http.StatusConflict {
		t.Fatalf("a taken alias must be 409, got %d (%v)", code, dup)
	}
	if code := s.call("POST", "/api/items", map[string]any{"kind": "task"}, nil); code != http.StatusBadRequest {
		t.Fatalf("a missing title must be 400, got %d", code)
	}
	if code := s.call("POST", "/api/items", map[string]any{"kind": "agent", "title": "x"}, nil); code != http.StatusBadRequest {
		t.Fatalf("agent items cannot be created here, got %d", code)
	}
	if got := s.items()["echoer"]; got.Status.State != "idle" {
		t.Fatalf("a new item is idle: %+v", got)
	}

	var run struct {
		RunID string `json:"runId"`
		State string `json:"state"`
	}
	if code := s.call("POST", "/api/items/echoer/run", map[string]any{}, &run); code != 200 || run.State != "running" {
		t.Fatalf("run: %d %+v", code, run)
	}
	if code := s.call("POST", "/api/items/echoer/run", map[string]any{}, nil); code != http.StatusConflict {
		t.Fatalf("a second run must be 409, got %d", code)
	}
	if got := s.items()["echoer"]; got.Status.State != "running" || got.Status.RunID != run.RunID {
		t.Fatalf("state %+v", got)
	}
	if code := s.call("DELETE", "/api/items/echoer", nil, nil); code != http.StatusConflict {
		t.Fatalf("deleting a running item must be 409, got %d", code)
	}

	if code := s.call("POST", "/api/items/echoer/send", map[string]any{"text": "hello api", "enter": true}, nil); code != 204 {
		t.Fatalf("send: %d", code)
	}
	waitFor(t, "the sent text to appear in the log", 10*time.Second, func() bool {
		_, b := s.raw("GET", "/api/runs/"+run.RunID+"/log?tail=50", nil, map[string]string{server.TokenHeader: s.srv.Token})
		return strings.Contains(string(b), "echo:hello api") && !strings.Contains(string(b), "\x1b")
	})

	var cmd map[string]string
	if code := s.call("GET", "/api/items/echoer/command?bare=1", nil, &cmd); code != 200 || cmd["text"] == "" {
		t.Fatalf("command: %d %v", code, cmd)
	}

	var term []string
	s.srv.OpenTerminal = func(title string, argv []string) error { term = append([]string{title}, argv...); return nil }
	if code := s.call("POST", "/api/items/echoer/terminal", nil, nil); code != 204 {
		t.Fatalf("terminal: %d", code)
	}
	want := []string{"echoer", "orchdock-cli", "--home", s.home, "attach", run.RunID}
	if strings.Join(term, "|") != strings.Join(want, "|") {
		t.Fatalf("terminal command %q, want %q", term, want)
	}

	var stopped struct{ State string }
	if code := s.call("POST", "/api/items/echoer/stop", map[string]any{"force": true}, &stopped); code != 200 || stopped.State != "ended" {
		t.Fatalf("stop: %d %+v", code, stopped)
	}
	if code := s.call("POST", "/api/items/echoer/stop", nil, nil); code != http.StatusNotFound {
		t.Fatalf("stopping an idle item must be 404, got %d", code)
	}
	var hist []map[string]any
	if code := s.call("GET", "/api/items/echoer/history", nil, &hist); code != 200 || len(hist) != 1 || hist[0]["outcome"] != "killed" {
		t.Fatalf("history: %d %v", code, hist)
	}
	if code := s.call("POST", "/api/items/echoer/terminal", nil, nil); code != http.StatusNotFound {
		t.Fatalf("a terminal for an idle item must be 404, got %d", code)
	}
}

func TestPatchDuplicateAndDelete(t *testing.T) {
	s := start(t, "")
	s.addItem("task", "base", "exit", "0")
	if code := s.call("PATCH", "/api/items/base", map[string]any{"title": "Renamed", "tags": []string{"a", "b"}}, nil); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	if code := s.call("PATCH", "/api/items/base", map[string]any{"kind": "service"}, nil); code != http.StatusBadRequest {
		t.Fatalf("the kind cannot change, got %d", code)
	}
	if code := s.call("PATCH", "/api/items/base", map[string]any{"alias": "BAD ALIAS"}, nil); code != http.StatusBadRequest {
		t.Fatalf("an invalid alias must be 400, got %d", code)
	}
	var dup itemJSON
	if code := s.call("POST", "/api/items/base/duplicate", nil, &dup); code != 201 || dup.Alias == "base" {
		t.Fatalf("duplicate: %d %+v", code, dup)
	}
	if code := s.call("PATCH", "/api/items/"+dup.Alias, map[string]any{"alias": "base"}, nil); code != http.StatusConflict {
		t.Fatalf("a taken alias must be 409, got %d", code)
	}
	if code := s.call("DELETE", "/api/items/"+dup.Alias, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code := s.call("DELETE", "/api/items/"+dup.Alias, nil, nil); code != 404 {
		t.Fatalf("delete of a removed item: %d", code)
	}
	if _, ok := s.items()["base"]; !ok {
		t.Fatal("the original must survive")
	}
}

func TestBulkPortsConfigAndNativeHooks(t *testing.T) {
	s := start(t, "")
	s.addItem("service", "one", "sleep", "")
	s.addItem("service", "two", "sleep", "")
	var res struct {
		Results []struct {
			Ref string
			OK  bool
		}
	}
	if code := s.call("POST", "/api/bulk", map[string]any{"action": "run", "refs": []string{"one", "two", "ghost"}}, &res); code != 200 {
		t.Fatalf("bulk run: %d", code)
	}
	oks := map[string]bool{}
	for _, r := range res.Results {
		oks[r.Ref] = r.OK
	}
	if !oks["one"] || !oks["two"] || oks["ghost"] {
		t.Fatalf("results %v", oks)
	}
	if code := s.call("POST", "/api/bulk", map[string]any{"action": "tag", "refs": []string{"one", "two"}, "tag": "web"}, nil); code != 200 {
		t.Fatalf("bulk tag: %d", code)
	}
	if code := s.call("POST", "/api/bulk", map[string]any{"action": "explode", "refs": []string{"one"}}, nil); code != 400 {
		t.Fatalf("unknown bulk action: %d", code)
	}
	if code := s.call("POST", "/api/bulk", map[string]any{"action": "stop", "refs": []string{"one", "two"}}, nil); code != 200 {
		t.Fatalf("bulk stop: %d", code)
	}
	for alias, it := range s.items() {
		if it.Status.State != "idle" {
			t.Errorf("%s still %s after a bulk stop", alias, it.Status.State)
		}
	}
	if code := s.call("POST", "/api/bulk", map[string]any{"action": "delete", "refs": []string{"two"}}, nil); code != 200 {
		t.Fatalf("bulk delete: %d", code)
	}
	if _, ok := s.items()["two"]; ok {
		t.Fatal("bulk delete must remove the item")
	}

	// Ports: a listener we own is reported as in use.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	var chk struct {
		Listening, Free bool
	}
	if code := s.call("GET", fmt.Sprintf("/api/ports/check?port=%d", port), nil, &chk); code != 200 || !chk.Listening || chk.Free {
		t.Fatalf("ports/check: %d %+v", code, chk)
	}
	if code := s.call("GET", "/api/ports/check?port=0", nil, nil); code != 400 {
		t.Fatalf("a bad port must be 400, got %d", code)
	}
	if code := s.call("PATCH", "/api/items/one", map[string]any{"service": map[string]any{"port": port, "portMode": "fixed"}}, nil); code != 200 {
		t.Fatalf("setting a port: %d", code)
	}
	var rows []struct {
		Alias     string
		Listening bool
	}
	if code := s.call("GET", "/api/ports", nil, &rows); code != 200 || len(rows) != 1 || !rows[0].Listening {
		t.Fatalf("ports: %d %+v", code, rows)
	}

	// Config round trip and validation.
	var cfg map[string]any
	if code := s.call("GET", "/api/config", nil, &cfg); code != 200 || cfg["uiPort"] == nil {
		t.Fatalf("config: %d %v", code, cfg)
	}
	cfg["uiPort"] = 80
	if code := s.call("PUT", "/api/config", cfg, nil); code != 400 {
		t.Fatalf("an invalid config must be 400, got %d", code)
	}
	cfg["uiPort"] = 7400
	cfg["theme"] = "dark"
	if code := s.call("PUT", "/api/config", cfg, nil); code != 200 {
		t.Fatalf("a valid config must be saved, got %d", code)
	}

	// Native helpers answer 501 without a shell, and work through one.
	for _, p := range []string{"/api/shell/pick-folder", "/api/shell/pick-file", "/api/shell/reveal", "/api/shell/open-url", "/api/ui/open", "/api/quit"} {
		if code := s.call("POST", p, map[string]any{"path": "x", "url": "https://x.invalid/"}, nil); code != http.StatusNotImplemented {
			t.Errorf("%s without a shell: %d, want 501", p, code)
		}
	}
	fake := &fakeShell{}
	s.srv.Shell = fake
	var picked map[string]string
	if code := s.call("POST", "/api/shell/pick-folder", map[string]any{"title": "Pick"}, &picked); code != 200 || picked["path"] != "/picked" {
		t.Fatalf("pick-folder: %d %v", code, picked)
	}
	if code := s.call("POST", "/api/shell/reveal", map[string]any{"path": "/some/dir"}, nil); code != 204 || fake.revealed != "/some/dir" {
		t.Fatalf("reveal: %d %q", code, fake.revealed)
	}
	if code := s.call("POST", "/api/shell/open-url", map[string]any{"url": "file:///etc/passwd"}, nil); code != 400 {
		t.Fatalf("only http(s) addresses may be opened, got %d", code)
	}
	if code := s.call("POST", "/api/shell/open-url", map[string]any{"url": "http://127.0.0.1:6420/"}, nil); code != 204 || fake.opened != "http://127.0.0.1:6420/" {
		t.Fatalf("open-url: %d %q", code, fake.opened)
	}
	opened := make(chan string, 1)
	s.srv.OpenUI = func(ref string) error { opened <- ref; return nil }
	if code := s.call("POST", "/api/ui/open", map[string]any{"itemRef": "one"}, nil); code != 204 || <-opened != "one" {
		t.Fatalf("ui/open: %d", code)
	}
	quit := make(chan struct{}, 1)
	s.srv.Quit = func() { quit <- struct{}{} }
	if code := s.call("POST", "/api/quit", nil, nil); code != 204 {
		t.Fatalf("quit: %d", code)
	}
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("quit was not called")
	}
}

type fakeShell struct{ revealed, opened string }

func (f *fakeShell) PickFolder(context.Context, string) (string, error) { return "/picked", nil }
func (f *fakeShell) PickFile(context.Context, string) (string, error)   { return "/picked.txt", nil }
func (f *fakeShell) Reveal(p string) error                              { f.revealed = p; return nil }
func (f *fakeShell) OpenURL(u string) error                             { f.opened = u; return nil }

// sse reads server-sent events on a goroutine.
type sse struct {
	events chan [2]string // type, data
	close  func()
}

func (s *svc) subscribe() *sse {
	s.t.Helper()
	resp, err := http.Get(s.base + "/api/events?t=" + s.srv.Token)
	if err != nil {
		s.t.Fatal(err)
	}
	out := &sse{events: make(chan [2]string, 256), close: func() { resp.Body.Close() }}
	s.t.Cleanup(out.close)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var typ string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				typ = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				out.events <- [2]string{typ, strings.TrimPrefix(line, "data: ")}
			}
		}
	}()
	waitForEvent(s.t, out, "hello", 5*time.Second, nil)
	return out
}

func waitForEvent(t *testing.T, e *sse, typ string, d time.Duration, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-e.events:
			if ev[0] != typ {
				continue
			}
			var m map[string]any
			_ = json.Unmarshal([]byte(ev[1]), &m)
			if match == nil || match(m) {
				return m
			}
		case <-deadline:
			t.Fatalf("no %q event within %s", typ, d)
		}
	}
}

func TestEventsAnnounceRunsStartingAndEnding(t *testing.T) {
	s := start(t, "")
	s.addItem("service", "evt", "sleep", "")
	ev := s.subscribe()
	var run struct{ RunID string }
	s.call("POST", "/api/items/evt/run", map[string]any{}, &run)
	waitForEvent(t, ev, "run.started", 5*time.Second, func(m map[string]any) bool { return m["runId"] == run.RunID })
	waitForEvent(t, ev, "item.changed", 5*time.Second, nil)
	s.call("POST", "/api/items/evt/stop", map[string]any{"force": true}, nil)
	waitForEvent(t, ev, "run.exited", 5*time.Second, func(m map[string]any) bool { return m["runId"] == run.RunID })
}

func TestAHostKilledByHandIsReportedInterruptedWithinThreeSeconds(t *testing.T) {
	s := start(t, "")
	s.addItem("service", "frail", "sleep", "")
	var run struct{ RunID string }
	s.call("POST", "/api/items/frail/run", map[string]any{}, &run)
	ev := s.subscribe()
	info, err := host.ReadInfo(filepath.Join(s.home, "runs", s.items()["frail"].ID, run.RunID))
	if err != nil {
		t.Fatal(err)
	}
	p, err := os.FindProcess(info.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil { // like a power loss: no result file
		t.Fatal(err)
	}
	waitForEvent(t, ev, "run.exited", 3*time.Second, func(m map[string]any) bool {
		return m["runId"] == run.RunID && m["reason"] == "interrupted"
	})
	if got := s.items()["frail"]; got.Status.State != "interrupted" {
		t.Fatalf("state %+v", got)
	}
}

func TestRunsSurviveAServiceRestartAndStayAttachable(t *testing.T) {
	home := t.TempDir()
	first := start(t, home)
	for _, a := range []string{"r1", "r2", "r3"} {
		first.addItem("service", a, "echo", "")
		if code := first.call("POST", "/api/items/"+a+"/run", map[string]any{}, nil); code != 200 {
			t.Fatalf("run %s: %d", a, code)
		}
	}
	first.stop()
	if _, err := os.Stat(filepath.Join(home, server.InfoFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("server.json must be removed when the service stops")
	}

	second := start(t, home)
	for _, a := range []string{"r1", "r2", "r3"} {
		it := second.items()[a]
		if it.Status.State != "running" {
			t.Fatalf("%s after the restart: %+v", a, it)
		}
		info, err := host.ReadInfo(filepath.Join(home, "runs", it.ID, it.Status.RunID))
		if err != nil {
			t.Fatal(err)
		}
		c, err := host.Dial(info, hello())
		if err != nil {
			t.Fatalf("%s must still be attachable: %v", a, err)
		}
		c.Close()
	}
}

func TestOnlyOneServicePerHome(t *testing.T) {
	s := start(t, "")
	store := catalog.New(s.home, nil)
	other := server.New(s.home, "test", runner.New(s.home, store, os.Args[0]), store)
	if _, err := other.Listen(); !errors.Is(err, server.ErrRunning) {
		t.Fatalf("a second service must be refused with ErrRunning, got %v", err)
	}
	in, alive := server.ReadInfo(s.home)
	if !alive || in.Port != s.srv.Port || in.Token != s.srv.Token {
		t.Fatalf("server.json %+v alive=%v", in, alive)
	}
	if st, err := os.Stat(filepath.Join(s.home, server.InfoFile)); err != nil || (st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("server.json must be private: %v %v", err, st)
	}
}

func hello() wire.HelloMsg { return wire.HelloMsg{Client: wire.ClientMonitor} }
