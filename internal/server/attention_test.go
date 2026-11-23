package server_test

import (
	"strings"
	"testing"
	"time"
)

type attnJSON struct {
	Alias     string `json:"alias"`
	Attention string `json:"attention"`
}

func (s *svc) attention() map[string]string {
	s.t.Helper()
	var st struct {
		Items []attnJSON `json:"items"`
	}
	s.call("GET", "/api/state", nil, &st)
	m := map[string]string{}
	for _, it := range st.Items {
		m[it.Alias] = it.Attention
	}
	return m
}

func TestABellRaisesAttentionAndOpeningClearsIt(t *testing.T) {
	s := start(t, "")
	ev := s.subscribe()
	s.addItem("agent", "asker", "bell", "")
	if code := s.call("POST", "/api/items/asker/run", map[string]any{}, nil); code != 200 {
		t.Fatalf("run: %d", code)
	}
	got := waitForEvent(t, ev, "attention", 15*time.Second, nil)
	if got["alias"] != "asker" {
		t.Fatalf("attention event: %v", got)
	}
	if a := s.attention()["asker"]; a == "" {
		t.Fatal("the item should carry its attention")
	}
	if code := s.call("POST", "/api/attention/asker/clear", nil, nil); code != 204 || s.attention()["asker"] != "" {
		t.Fatalf("clear: %d", code)
	}
	s.call("POST", "/api/items/asker/stop", map[string]any{"force": true}, nil)
}

func TestFailuresAndNotifyRaiseAttention(t *testing.T) {
	s := start(t, "")
	s.subscribe() // poll every second
	s.addItem("task", "flaky", "late-exit", "4")
	s.addItem("task", "fine", "late-exit", "0")
	for _, a := range []string{"flaky", "fine"} {
		if code := s.call("POST", "/api/items/"+a+"/run", map[string]any{}, nil); code != 200 {
			t.Fatalf("run %s: %d", a, code)
		}
	}
	waitFor(t, "attention for the failed task", 20*time.Second, func() bool { return strings.Contains(s.attention()["flaky"], "exit 4") })
	if a := s.attention()["fine"]; a != "" {
		t.Fatalf("a task that succeeded raised %q", a)
	}
	if code := s.call("POST", "/api/attention/fine", map[string]any{"reason": "build finished"}, nil); code != 204 || s.attention()["fine"] != "build finished" {
		t.Fatalf("notify: %d %q", code, s.attention()["fine"])
	}
	if code := s.call("POST", "/api/attention/nobody", map[string]any{}, nil); code != 404 {
		t.Fatalf("notify an unknown item: %d", code)
	}
}

func TestSchedulePreviewAndAutostartStatus(t *testing.T) {
	s := start(t, "")
	var p struct {
		Spec string      `json:"spec"`
		Next []time.Time `json:"next"`
	}
	if code := s.call("GET", "/api/schedule/preview?spec=daily%2009:00", nil, &p); code != 200 || p.Spec != "daily 09:00" || len(p.Next) != 3 {
		t.Fatalf("preview: %d %+v", code, p)
	}
	for i, n := range p.Next {
		if l := n.Local(); l.Hour() != 9 || (i > 0 && !n.After(p.Next[i-1])) {
			t.Fatalf("slot %d: %s", i, n)
		}
	}
	if code := s.call("GET", "/api/schedule/preview?spec=hourly", nil, nil); code != 400 {
		t.Fatalf("bad spec: %d", code)
	}
	var a struct {
		Supported bool `json:"supported"`
	}
	if code := s.call("GET", "/api/autostart", nil, &a); code != 200 || a.Supported {
		t.Fatalf("without a program to start, autostart is not offered: %d %+v", code, a)
	}
	if code := s.call("PUT", "/api/autostart", map[string]any{"enabled": true}, nil); code != 501 {
		t.Fatalf("setting it without a program: %d", code)
	}
}
