package server_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/server"
)

type healthJSON struct {
	Alias  string `json:"alias"`
	Health string `json:"health"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
	Service struct {
		Port int `json:"port"`
	} `json:"service"`
}

func (s *svc) services() map[string]healthJSON {
	s.t.Helper()
	var st struct {
		Items []healthJSON `json:"items"`
	}
	s.call("GET", "/api/state", nil, &st)
	m := map[string]healthJSON{}
	for _, it := range st.Items {
		m[it.Alias] = it
	}
	return m
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestServiceHealthIsShown(t *testing.T) {
	s := start(t, "")
	s.subscribe() // a listener makes the watcher poll every second
	body := testItem(t, "service", "web", "listen", "{port}")
	body["service"] = map[string]any{"portMode": "fixed", "port": freePort(t)}
	if code := s.call("POST", "/api/items", body, nil); code != 201 {
		t.Fatalf("add: %d", code)
	}
	if code := s.call("POST", "/api/items/web/run", map[string]any{}, nil); code != 200 {
		t.Fatalf("run: %d", code)
	}
	waitFor(t, "the service to be healthy", 15*time.Second, func() bool { return s.services()["web"].Health == "healthy" })
	if code := s.call("POST", "/api/items/web/stop", map[string]any{"force": true}, nil); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	waitFor(t, "health to be cleared", 15*time.Second, func() bool { return s.services()["web"].Health == "" })
}

func TestRestartPolicyRestartsAFailedService(t *testing.T) {
	s := start(t, "")
	s.subscribe()
	body := testItem(t, "service", "flaky", "late-exit", "3")
	body["service"] = map[string]any{"restart": "on-failure"}
	if code := s.call("POST", "/api/items", body, nil); code != 201 {
		t.Fatalf("add: %d", code)
	}
	if code := s.call("POST", "/api/items/flaky/run", map[string]any{}, nil); code != 200 {
		t.Fatalf("run: %d", code)
	}
	var hist []struct {
		Trigger string `json:"trigger"`
		Outcome string `json:"outcome"`
	}
	waitFor(t, "an automatic restart", 25*time.Second, func() bool {
		s.call("GET", "/api/items/flaky/history", nil, &hist)
		for _, h := range hist {
			if h.Trigger == "restart-policy" {
				return true
			}
		}
		return false
	})
	if hist[0].Outcome != "failed" {
		t.Fatalf("the first run should have failed: %+v", hist)
	}
	// a user stop is never undone
	waitFor(t, "the restarted run", 10*time.Second, func() bool { return s.services()["flaky"].Status.State == "running" })
	s.call("POST", "/api/items/flaky/stop", map[string]any{"force": true}, nil)
}

func TestPortsNameAForeignHolder(t *testing.T) {
	s := start(t, "")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	for _, alias := range []string{"a", "b"} {
		body := testItem(t, "service", alias, "listen", "{port}")
		body["service"] = map[string]any{"portMode": "fixed", "port": port}
		if code := s.call("POST", "/api/items", body, nil); code != 201 {
			t.Fatalf("add %s: %d", alias, code)
		}
	}
	var rows []struct {
		Alias     string `json:"alias"`
		Listening bool   `json:"listening"`
		Running   bool   `json:"running"`
		Holder    string `json:"holder"`
		Conflict  bool   `json:"conflict"`
	}
	if code := s.call("GET", "/api/ports", nil, &rows); code != 200 || len(rows) != 2 {
		t.Fatalf("ports: %d %+v", code, rows)
	}
	for _, r := range rows {
		if !r.Listening || r.Running || r.Holder == "" || !r.Conflict {
			t.Fatalf("row %+v: want listening, not running, a holder and a conflict", r)
		}
	}
	if code := s.call("POST", "/api/items/a/run", map[string]any{}, nil); code != 409 {
		t.Fatalf("starting on a held port: %d", code)
	}
}

func TestScheduledTasksReportTheirNextRun(t *testing.T) {
	s := start(t, "")
	body := map[string]any{"kind": "task", "title": "Pull", "alias": "pull", "command": map[string]any{"line": "git pull"},
		"task": map[string]any{"missed": "skip", "overlap": "skip", "schedule": map[string]any{"enabled": true, "kind": "daily", "times": []string{"09:00"}}}}
	if code := s.call("POST", "/api/items", body, nil); code != 201 {
		t.Fatalf("add: %d", code)
	}
	var st struct {
		Items []struct {
			NextRun time.Time `json:"nextRun"`
		} `json:"items"`
	}
	waitFor(t, "the next run", 10*time.Second, func() bool {
		s.call("GET", "/api/state", nil, &st)
		return len(st.Items) == 1 && !st.Items[0].NextRun.IsZero()
	})
	if n := st.Items[0].NextRun.Local(); n.Hour() != 9 || n.Minute() != 0 || !n.After(time.Now()) {
		t.Fatalf("next run: %s", n)
	}
}

func TestScheduleAsText(t *testing.T) {
	s := start(t, "")
	type item struct {
		Task struct {
			Schedule *struct {
				Kind    string `json:"kind"`
				Every   string `json:"every"`
				Enabled bool   `json:"enabled"`
			} `json:"schedule"`
		} `json:"task"`
	}
	var it item
	body := map[string]any{"kind": "task", "title": "Sync", "alias": "sync", "command": map[string]any{"line": "x"}, "scheduleSpec": "every 2h"}
	if code := s.call("POST", "/api/items", body, &it); code != 201 || it.Task.Schedule == nil || it.Task.Schedule.Every != "2h" || !it.Task.Schedule.Enabled {
		t.Fatalf("create with a schedule: %d %+v", code, it.Task.Schedule)
	}
	if code := s.call("PATCH", "/api/items/sync", map[string]any{"scheduleSpec": "off"}, &it); code != 200 || it.Task.Schedule.Enabled {
		t.Fatalf("pause: %d %+v", code, it.Task.Schedule)
	}
	var removed item
	if code := s.call("PATCH", "/api/items/sync", map[string]any{"scheduleSpec": ""}, &removed); code != 200 || removed.Task.Schedule != nil {
		t.Fatalf("remove: %d %+v", code, removed.Task.Schedule)
	}
	if code := s.call("PATCH", "/api/items/sync", map[string]any{"scheduleSpec": "now and then"}, nil); code != 400 {
		t.Fatalf("bad schedule: %d", code)
	}
	svc := map[string]any{"kind": "service", "title": "Web", "alias": "web", "command": map[string]any{"line": "x"}, "scheduleSpec": "daily 09:00"}
	if code := s.call("POST", "/api/items", svc, nil); code != 400 {
		t.Fatalf("a service with a schedule: %d", code)
	}
}

func TestARunningServiceGetsItsOwnWebIcon(t *testing.T) {
	s := start(t, "")
	s.subscribe()
	body := testItem(t, "service", "site", "listen", "{port}")
	body["service"] = map[string]any{"portMode": "fixed", "port": freePort(t)}
	if code := s.call("POST", "/api/items", body, nil); code != 201 {
		t.Fatalf("add: %d", code)
	}
	if code := s.call("POST", "/api/items/site/run", map[string]any{}, nil); code != 200 {
		t.Fatalf("run: %d", code)
	}
	var fav string
	waitFor(t, "the web icon", 20*time.Second, func() bool {
		var st struct {
			Items []struct {
				Favicon string `json:"favicon"`
			} `json:"items"`
		}
		s.call("GET", "/api/state", nil, &st)
		fav = st.Items[0].Favicon
		return fav != ""
	})
	if !strings.HasPrefix(fav, "favicon-") || !strings.HasSuffix(fav, ".png") {
		t.Fatalf("icon file: %q", fav)
	}
	code, b := s.raw("GET", "/api/icons/"+fav, nil, map[string]string{server.TokenHeader: s.srv.Token})
	if code != 200 || !strings.HasPrefix(string(b), "\x89PNG") {
		t.Fatalf("serving the icon: %d", code)
	}
	s.call("POST", "/api/items/site/stop", map[string]any{"force": true}, nil)
}
