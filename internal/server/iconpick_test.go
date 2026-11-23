package server_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)

func TestIconUploadListAndTagAutoIcon(t *testing.T) {
	s := start(t, "")
	up := func(name string, b []byte) (int, string) {
		var r struct {
			Name string `json:"name"`
		}
		code := s.call("POST", "/api/icons", map[string]any{"name": name, "data": base64.StdEncoding.EncodeToString(b)}, &r)
		return code, r.Name
	}
	code, name := up("My Logo!.PNG", pngBytes)
	if code != 201 || !strings.HasPrefix(name, "my-logo-") || !strings.HasSuffix(name, ".png") {
		t.Fatalf("upload: %d %q", code, name)
	}
	if code, _ := up("evil.svg", []byte(`<svg onload="alert(1)"/>`)); code != 400 {
		t.Fatalf("unsafe svg: %d", code)
	}
	if code, _ := up("doc.txt", []byte("hello")); code != 400 {
		t.Fatalf("not an image: %d", code)
	}
	if code, _ := up("big.png", append(pngBytes, make([]byte, 600<<10)...)); code != 400 {
		t.Fatalf("too large: %d", code)
	}
	var list struct {
		Icons []string `json:"icons"`
	}
	if s.call("GET", "/api/icons", nil, &list); len(list.Icons) != 1 || list.Icons[0] != name {
		t.Fatalf("list: %+v", list)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public", "favicon.ico"), append([]byte{0, 0, 1, 0}, make([]byte, 30)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := s.call("PUT", "/api/tags/shop", map[string]any{"path": dir}, nil); code != 200 {
		t.Fatalf("tag: %d", code)
	}
	if code := s.call("PUT", "/api/tags/shop", map[string]any{"icon": map[string]any{"primary": "auto"}}, nil); code != 200 {
		t.Fatalf("auto icon: %d", code)
	}
	var st struct {
		Tags []struct {
			AutoIcon string `json:"autoIcon"`
		} `json:"tags"`
	}
	s.call("GET", "/api/state", nil, &st)
	if len(st.Tags) != 1 || !strings.HasSuffix(st.Tags[0].AutoIcon, ".ico") {
		t.Fatalf("auto icon: %+v", st.Tags)
	}
	if s.call("GET", "/api/icons", nil, &list); len(list.Icons) != 1 {
		t.Fatalf("project icons are not offered as uploads: %+v", list)
	}
	if code := s.call("PUT", "/api/tags/shop", map[string]any{"icon": map[string]any{"primary": "project"}}, nil); code != 400 {
		t.Fatalf("an item-only icon on a project: %d", code)
	}
}
