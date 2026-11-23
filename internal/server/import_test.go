package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohamed-foly/orchdock/internal/server"
)

func TestImportAndIconsThroughTheAPI(t *testing.T) {
	s := start(t, "")
	img := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(img, []byte("\x89PNG fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := map[string]any{
		"iconFiles": map[string]string{"shop.png": img},
		"projects":  []any{map[string]any{"name": "shop", "path": "", "icon": map[string]any{"primary": "file:shop.png"}}},
		"items": []any{
			map[string]any{"alias": "shop-dev", "title": "Dev", "kind": "service", "project": "shop", "command": map[string]any{"line": "dev --port {port}"}, "service": map[string]any{"port": 6600}},
			map[string]any{"alias": "broken", "title": "Broken", "kind": "task"},
		},
	}
	type report struct {
		DryRun   bool                    `json:"dryRun"`
		Created  []string                `json:"created"`
		Rejected []struct{ Name string } `json:"rejected"`
		Icons    []string                `json:"icons"`
	}
	var dry report
	if code := s.call("POST", "/api/import", map[string]any{"dryRun": true, "file": file}, &dry); code != 200 || !dry.DryRun || len(dry.Created) != 1 {
		t.Fatalf("dry run: %d %+v", code, dry)
	}
	if len(s.items()) != 0 {
		t.Fatal("the dry run created items")
	}
	var rep report
	if code := s.call("POST", "/api/import", map[string]any{"file": file}, &rep); code != 200 || len(rep.Created) != 1 || len(rep.Rejected) != 1 || len(rep.Icons) != 1 {
		t.Fatalf("import: %d %+v", code, rep)
	}
	if _, ok := s.items()["shop-dev"]; !ok {
		t.Fatal("shop-dev missing after import")
	}
	if code := s.call("POST", "/api/import", map[string]any{"file": map[string]any{}}, nil); code != 400 {
		t.Fatalf("empty file: %d", code)
	}

	code, body := s.raw("GET", "/api/icons/shop.png", nil, map[string]string{server.TokenHeader: s.srv.Token})
	if code != 200 || !strings.HasPrefix(string(body), "\x89PNG") {
		t.Fatalf("icon: %d %q", code, body)
	}
	if code, _ := s.raw("GET", "/api/icons/shop.png", nil, nil); code != 403 {
		t.Fatalf("icon without the token: %d", code)
	}
	for _, bad := range []string{"Shop.PNG", "x.exe", "..%5Csecret.png"} {
		if code, _ := s.raw("GET", "/api/icons/"+bad, nil, map[string]string{server.TokenHeader: s.srv.Token}); code != 400 && code != 404 {
			t.Errorf("icon %q: %d", bad, code)
		}
	}
	if code, _ := s.raw("GET", "/api/icons/none.png", nil, map[string]string{server.TokenHeader: s.srv.Token}); code != 404 {
		t.Errorf("missing icon: %d", code)
	}
}

func TestOpenALink(t *testing.T) {
	s := start(t, "")
	fake := &fakeShell{}
	s.srv.Shell = fake
	dir := t.TempDir()
	file := filepath.Join(dir, "tool.exe")
	if err := os.WriteFile(file, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := func(alias, target string) {
		if code := s.call("POST", "/api/items", map[string]any{"kind": "link", "title": alias, "alias": alias, "link": map[string]any{"target": target}}, nil); code != 201 {
			t.Fatalf("add %s: %d", alias, code)
		}
	}
	link("site", "https://example.com/docs")
	link("folder", dir)
	link("file", file)
	link("gone", filepath.Join(dir, "missing"))
	if code := s.call("POST", "/api/items/site/open", nil, nil); code != 204 || fake.opened != "https://example.com/docs" {
		t.Fatalf("open url: %d %q", code, fake.opened)
	}
	if code := s.call("POST", "/api/items/folder/open", nil, nil); code != 204 || fake.opened != dir {
		t.Fatalf("open folder: %d %q", code, fake.opened)
	}
	fake.opened = ""
	if code := s.call("POST", "/api/items/file/open", nil, nil); code != 204 || fake.revealed != file || fake.opened != "" {
		t.Fatalf("a file must be revealed, never run: %d revealed=%q opened=%q", code, fake.revealed, fake.opened)
	}
	if code := s.call("POST", "/api/items/gone/open", nil, nil); code != 404 {
		t.Fatalf("missing target: %d", code)
	}
	s.addItem("task", "job", "echo", "x")
	if code := s.call("POST", "/api/items/job/open", nil, nil); code != 400 {
		t.Fatalf("open a task: %d", code)
	}
}
