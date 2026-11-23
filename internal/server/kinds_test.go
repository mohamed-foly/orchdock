package server_test

import "testing"

func TestKindsThroughTheAPI(t *testing.T) {
	s := start(t, "")
	var mcp struct {
		ID   string `json:"id"`
		Base string `json:"base"`
	}
	if code := s.call("POST", "/api/kinds", map[string]any{"name": "MCP server", "base": "service", "color": "#db2777"}, &mcp); code != 201 || mcp.Base != "service" {
		t.Fatalf("create: %d %+v", code, mcp)
	}
	if code := s.call("POST", "/api/kinds", map[string]any{"name": "mcp SERVER", "base": "task"}, nil); code != 409 {
		t.Fatalf("duplicate name: %d", code)
	}
	type item struct {
		Kind   string `json:"kind"`
		KindID string `json:"kindId"`
	}
	var it item
	body := map[string]any{"kind": "MCP server", "title": "Images", "alias": "images", "command": map[string]any{"line": "node x.js"}}
	if code := s.call("POST", "/api/items", body, &it); code != 201 || it.Kind != "service" || it.KindID != mcp.ID {
		t.Fatalf("create an item of a user kind: %d %+v", code, it)
	}
	if code := s.call("PATCH", "/api/kinds/task", map[string]any{"name": "Jobs"}, nil); code != 200 {
		t.Fatalf("rename a built-in kind: %d", code)
	}
	if code := s.call("PATCH", "/api/kinds/"+mcp.ID, map[string]any{"base": "task"}, nil); code != 400 {
		t.Fatalf("changing the base: %d", code)
	}
	var st struct {
		Kinds []struct{ ID, Name string } `json:"kinds"`
	}
	s.call("GET", "/api/state", nil, &st)
	if len(st.Kinds) != 6 || st.Kinds[2].Name != "Jobs" || st.Kinds[5].ID != mcp.ID {
		t.Fatalf("kinds in the state: %+v", st.Kinds)
	}
	if code := s.call("DELETE", "/api/kinds/service", nil, nil); code != 400 {
		t.Fatalf("delete a built-in kind: %d", code)
	}
	if code := s.call("DELETE", "/api/kinds/"+mcp.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var after struct {
		Items []item `json:"items"`
	}
	s.call("GET", "/api/state", nil, &after)
	if after.Items[0].KindID != "" || after.Items[0].Kind != "service" {
		t.Fatalf("items of a deleted kind become plain services: %+v", after.Items[0])
	}
}
