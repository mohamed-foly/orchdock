package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteFileAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.json")
	for _, content := range []string{"one", "two"} {
		if err := WriteFileAtomic(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil || string(b) != content {
			t.Fatalf("got %q %v want %q", b, err, content)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestLockExcludesAndBreaksStale(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	l, err := Lock(p, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(p, 30*time.Millisecond, time.Minute); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("second lock should time out, got %v", err)
	}
	if err := l.Unlock(); err != nil {
		t.Fatal(err)
	}
	// A lock file older than the stale age is taken over.
	if err := os.WriteFile(p, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	l2, err := Lock(p, 100*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
	_ = l2.Unlock()
	var nilLock *FileLock
	if err := nilLock.Unlock(); err != nil {
		t.Fatal(err)
	}
}
