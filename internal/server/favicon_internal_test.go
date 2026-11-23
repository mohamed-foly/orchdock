package server

import (
	"net/url"
	"testing"
)

func TestIconLinksBestFirst(t *testing.T) {
	html := []byte(`<head><link rel="stylesheet" href="/a.css"><link rel="apple-touch-icon" href="/apple.png">
<link REL='shortcut icon' href='/old.ico'><link href="/favicon-abc.png" rel="icon" type="image/png"></head>`)
	got := iconLinks(html)
	want := []string{"/favicon-abc.png", "/old.ico", "/apple.png"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOnlyLocalAddressesAreRead(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://127.0.0.1:6420/": true, "http://localhost:3000/x": true, "https://[::1]:8443/": true,
		"http://example.com/favicon.ico": false, "http://10.0.0.5/": false, "file:///etc/passwd": false, "ftp://127.0.0.1/": false,
	} {
		u, _ := url.Parse(raw)
		if local(u) != ok {
			t.Errorf("local(%s) = %v", raw, !ok)
		}
	}
}

func TestImageExt(t *testing.T) {
	cases := map[string]string{
		"\x89PNG\r\n\x1a\nrest":       ".png",
		"\x00\x00\x01\x00rest":        ".ico",
		"\xff\xd8\xffrest":            ".jpg",
		`<?xml version="1.0"?><svg/>`: ".svg",
		"<html>not an image</html>":   "",
	}
	for in, want := range cases {
		if got := imageExt([]byte(in), ""); got != want {
			t.Errorf("imageExt(%q) = %q, want %q", in, got, want)
		}
	}
}
