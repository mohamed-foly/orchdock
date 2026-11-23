package server_test

import (
	"strings"
	"testing"

	"github.com/mohamed-foly/orchdock/internal/server"
)

func TestTagsViewsTrashAndExport(t *testing.T) {
	s := start(t, "")
	item := map[string]any{"kind": "task", "title": "Pull", "alias": "pull", "tags": []string{"daily"},
		"command": map[string]any{"line": "git pull", "env": map[string]string{"TOKEN": "hush-hush"}}}
	if code := s.call("POST", "/api/items", item, nil); code != 201 {
		t.Fatalf("add: %d", code)
	}
	if code := s.call("PUT", "/api/tags/daily", map[string]any{"color": "#8b5cf6"}, nil); code != 200 {
		t.Fatalf("tag colour: %d", code)
	}
	if code := s.call("PUT", "/api/tags/daily", map[string]any{"color": "purple"}, nil); code != 400 {
		t.Fatalf("bad colour: %d", code)
	}
	var v struct{ ID string }
	if code := s.call("POST", "/api/views", map[string]any{"name": "Daily", "query": "tag:daily", "group": "kind"}, &v); code != 201 || v.ID == "" {
		t.Fatalf("view: %d", code)
	}
	if code := s.call("POST", "/api/views", map[string]any{"name": "daily", "query": "tag:daily is:failed"}, nil); code != 201 {
		t.Fatalf("replace view: %d", code)
	}
	var st struct {
		Tags  []struct{ Name, Color string } `json:"tags"`
		Views []struct{ Name, Query string }
		Trash int `json:"trash"`
	}
	s.call("GET", "/api/state", nil, &st)
	if len(st.Tags) != 1 || st.Tags[0].Color != "#8b5cf6" || len(st.Views) != 1 || st.Views[0].Query != "tag:daily is:failed" {
		t.Fatalf("state: %+v", st)
	}
	if code := s.call("DELETE", "/api/views/"+v.ID, nil, nil); code != 204 {
		t.Fatalf("delete view: %d", code)
	}

	code, body := s.raw("GET", "/api/export", nil, map[string]string{server.TokenHeader: s.srv.Token})
	if code != 200 || strings.Contains(string(body), "hush-hush") || !strings.Contains(string(body), `"TOKEN"`) {
		t.Fatalf("export: %d, redacted: %v", code, !strings.Contains(string(body), "hush-hush"))
	}
	if _, body := s.raw("GET", "/api/export?includeEnv=1", nil, map[string]string{server.TokenHeader: s.srv.Token}); !strings.Contains(string(body), "hush-hush") {
		t.Fatal("includeEnv=1 keeps the values")
	}

	if code := s.call("DELETE", "/api/items/pull", nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var trash []struct{ Alias string }
	if s.call("GET", "/api/trash", nil, &trash); len(trash) != 1 || trash[0].Alias != "pull" {
		t.Fatalf("trash: %+v", trash)
	}
	if code := s.call("POST", "/api/trash/pull/restore", nil, nil); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	if _, ok := s.items()["pull"]; !ok {
		t.Fatal("restored item missing")
	}
	s.call("DELETE", "/api/items/pull", nil, nil)
	if code := s.call("DELETE", "/api/trash", nil, nil); code != 204 {
		t.Fatalf("empty trash: %d", code)
	}
	if s.call("GET", "/api/trash", nil, &trash); len(trash) != 0 {
		t.Fatalf("trash after emptying: %+v", trash)
	}
}

func TestSectionsCanBeRenamedAndReordered(t *testing.T) {
	s := start(t, "")
	var a, b struct{ ID string }
	s.call("POST", "/api/views", map[string]any{"name": "Shop", "query": "tag:shop"}, &a)
	s.call("POST", "/api/views", map[string]any{"name": "Lab", "query": "tag:lab"}, &b)
	if code := s.call("PATCH", "/api/views/"+a.ID, map[string]any{"name": "Store", "query": "tag:store"}, nil); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	if code := s.call("PATCH", "/api/views/"+a.ID, map[string]any{"name": " "}, nil); code != 400 {
		t.Fatalf("empty name: %d", code)
	}
	if code := s.call("POST", "/api/views/"+b.ID+"/move", map[string]any{"by": -1}, nil); code != 204 {
		t.Fatalf("move: %d", code)
	}
	var st struct {
		Views []struct{ Name, Query string }
	}
	s.call("GET", "/api/state", nil, &st)
	if len(st.Views) != 2 || st.Views[0].Name != "Lab" || st.Views[1].Name != "Store" || st.Views[1].Query != "tag:store" {
		t.Fatalf("sections: %+v", st.Views)
	}
}
