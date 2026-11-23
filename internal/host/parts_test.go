package host

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRingNotWrapped(t *testing.T) {
	r := newRing(16)
	r.Write([]byte("hello "))
	r.Write([]byte("world"))
	if got := string(r.Snapshot()); got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestRingWrapStartsAfterNewline(t *testing.T) {
	r := newRing(20)
	r.Write([]byte("first line\nsecond line\nthird"))
	got := string(r.Snapshot())
	// 28 bytes into 20: the oldest 8 are gone ("first li"), the partial line is dropped.
	if got != "second line\nthird" {
		t.Fatalf("got %q", got)
	}
}

func TestRingWrapWithoutNewline(t *testing.T) {
	r := newRing(8)
	r.Write([]byte("abcdefghijkl"))
	if got := string(r.Snapshot()); got != "efghijkl" {
		t.Fatalf("got %q", got)
	}
}

func TestRingLargeWriteKeepsTail(t *testing.T) {
	r := newRing(10)
	r.Write(bytes.Repeat([]byte("x"), 5))
	r.Write([]byte("0123456789ABCDEFGH\nlast"))
	got := r.Snapshot()
	if !bytes.HasSuffix(got, []byte("last")) || len(got) > 10 {
		t.Fatalf("got %q", got)
	}
}

func TestRingZeroSizeIsSafe(t *testing.T) {
	r := newRing(0)
	r.Write([]byte("abc"))
	if len(r.Snapshot()) > 1 {
		t.Fatal("size must be clamped to 1")
	}
}

func TestBellScanner(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   bool
	}{
		{"bare bell", []string{"done\a"}, true},
		{"no bell", []string{"plain text"}, false},
		{"osc title with BEL terminator", []string{"\x1b]0;my title\a"}, false},
		{"osc title with ST terminator", []string{"\x1b]0;my title\x1b\\"}, false},
		{"osc then real bell", []string{"\x1b]0;t\a\a"}, true},
		{"osc split across chunks", []string{"\x1b]0;ti", "tle\a"}, false},
		{"esc split from bracket", []string{"\x1b", "]2;x\a"}, false},
		{"bell after split osc", []string{"\x1b]0;a", "b\a", "\a"}, true},
		{"bell inside DCS is data", []string{"\x1bPq\a\x1b\\"}, false},
		{"bell after DCS ends", []string{"\x1bPq\x1b\\\a"}, true},
		{"csi sequence then bell", []string{"\x1b[31mred\x1b[0m\a"}, true},
		{"osc aborted by new esc", []string{"\x1b]0;x\x1b[0m\a"}, true},
		{"esc esc bracket", []string{"\x1b\x1b]0;x\a"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s bellScanner
			got := false
			for _, ch := range c.chunks {
				if s.Feed([]byte(ch)) {
					got = true
				}
			}
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCapLogTrimsToNewestHalf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	l, err := openCapLog(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	clock := time.Unix(1000, 0)
	l.now = func() time.Time { return clock }
	for i := 0; i < 5; i++ {
		if _, err := l.Write(bytes.Repeat([]byte{byte('a' + i)}, 30)); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(10 * time.Second) // beyond the trim gap
	}
	if _, err := l.Write([]byte("END")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if int64(len(b)) > 100 || !bytes.HasSuffix(b, []byte("END")) {
		t.Fatalf("size %d, tail %q", len(b), b[max(0, len(b)-10):])
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
}

func TestCapLogTrimIsRateLimited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	l, err := openCapLog(path, 50)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	clock := time.Unix(5000, 0)
	l.now = func() time.Time { return clock }
	l.Write(bytes.Repeat([]byte("a"), 60)) // trims: first cut is allowed
	l.Write(bytes.Repeat([]byte("b"), 60)) // within the gap: grows instead
	b, _ := os.ReadFile(path)
	if len(b) <= 50 {
		t.Fatalf("a second trim inside the gap must not happen (size %d)", len(b))
	}
}

func TestCapLogUnlimited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	l, err := openCapLog(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Write(bytes.Repeat([]byte("z"), 5000))
	if st, _ := os.Stat(path); st.Size() != 5000 {
		t.Fatalf("size %d", st.Size())
	}
}

func TestCapLogAppendsToExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	os.WriteFile(path, []byte("old"), 0o600)
	l, err := openCapLog(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Write([]byte("new"))
	l.Close()
	if b, _ := os.ReadFile(path); string(b) != "oldnew" {
		t.Fatalf("got %q", b)
	}
}
