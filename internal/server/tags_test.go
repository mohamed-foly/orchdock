package server_test

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestTagsAPIAndFolderSuggestions(t *testing.T) {
	s := start(t, "")
	root := t.TempDir()
	type tag struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		Color string `json:"color"`
	}
	var d tag
	if code := s.call("PUT", "/api/tags/shop", map[string]any{"path": root, "color": "#4f8cff"}, &d); code != 200 || d.Path != root {
		t.Fatalf("define: %d %+v", code, d)
	}
	if code := s.call("PUT", "/api/tags/shop", map[string]any{"color": "red"}, nil); code != 400 {
		t.Fatalf("bad colour: %d", code)
	}
	if code := s.call("PUT", "/api/tags/Not%20A%20Tag", map[string]any{"color": "#000000"}, nil); code != 400 {
		t.Fatalf("bad name: %d", code)
	}
	if code := s.call("PUT", "/api/tags/lab", map[string]any{"path": "relative"}, nil); code != 400 {
		t.Fatalf("relative folder: %d", code)
	}

	var sug struct {
		Tags []string `json:"tags"`
	}
	s.call("GET", "/api/tags/suggest?cwd="+filepath.Join(root, "web"), nil, &sug)
	if !slices.Equal(sug.Tags, []string{"shop"}) {
		t.Fatalf("suggest: %+v", sug)
	}
	s.call("GET", "/api/tags/suggest?cwd="+t.TempDir(), nil, &sug)
	if len(sug.Tags) != 0 {
		t.Fatalf("no suggestion outside: %+v", sug)
	}

	// creating an item inside the folder does not tag it by itself
	type item struct {
		Alias string   `json:"alias"`
		Tags  []string `json:"tags"`
	}
	var in item
	body := map[string]any{"kind": "task", "title": "Inside", "alias": "inside", "command": map[string]any{"line": "echo hi", "cwd": filepath.Join(root, "web")}}
	if code := s.call("POST", "/api/items", body, &in); code != 201 || len(in.Tags) != 0 {
		t.Fatalf("create: %d %+v", code, in)
	}
	s.call("PATCH", "/api/items/inside", map[string]any{"tags": []string{"shop", "daily"}}, nil)

	if code := s.call("PUT", "/api/tags/shop", map[string]any{"name": "store"}, &d); code != 200 || d.Name != "store" || d.Path != root {
		t.Fatalf("rename: %d %+v", code, d)
	}
	var st struct {
		Tags  []tag  `json:"tags"`
		Items []item `json:"items"`
	}
	s.call("GET", "/api/state", nil, &st)
	if len(st.Tags) != 1 || st.Tags[0].Name != "store" || !slices.Equal(st.Items[0].Tags, []string{"store", "daily"}) {
		t.Fatalf("after rename: %+v %+v", st.Tags, st.Items)
	}
	if code := s.call("DELETE", "/api/tags/store", nil, nil); code != 204 {
		t.Fatalf("delete definition: %d", code)
	}
	s.call("GET", "/api/state", nil, &st)
	if len(st.Tags) != 0 || !slices.Contains(st.Items[0].Tags, "store") {
		t.Fatalf("deleting the definition keeps the tag on items: %+v %+v", st.Tags, st.Items)
	}
	if code := s.call("DELETE", "/api/tags/store?fromItems=1", nil, nil); code != 204 {
		t.Fatalf("delete from items: %d", code)
	}
	s.call("GET", "/api/state", nil, &st)
	if slices.Contains(st.Items[0].Tags, "store") {
		t.Fatalf("fromItems removes it: %+v", st.Items)
	}
}
