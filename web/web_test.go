package web

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestIndexEmbedded(t *testing.T) {
	b, err := fs.ReadFile(FS, "index.html")
	if err != nil || !strings.Contains(string(b), "<title>OrchDock</title>") {
		t.Fatalf("index.html not embedded: %v", err)
	}
}

func TestAgentLogosAreEmbeddedWithTheirNotice(t *testing.T) {
	for _, name := range []string{"icons/claudecode-color.svg", "icons/codex-color.svg"} {
		b, err := fs.ReadFile(FS, name)
		if err != nil || !strings.HasPrefix(string(b), "<svg") || strings.Contains(strings.ToLower(string(b)), "<script") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	notice, err := os.ReadFile("icons/NOTICE.md")
	if err != nil || !strings.Contains(string(notice), "claudecode-color.svg") || !strings.Contains(string(notice), "codex-color.svg") || !strings.Contains(string(notice), "Trademarks") {
		t.Fatalf("icons/NOTICE.md must list both logos and the trademark note: %v", err)
	}
}
