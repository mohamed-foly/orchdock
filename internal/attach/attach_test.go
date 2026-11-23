package attach

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

func TestAttachLoopForwardsResizesAndStopsOnInputEnd(t *testing.T) {
	// A fake host side: the other end of a pipe pair.
	a, b := fakeConnPair(t)
	var sizes = [][2]int{{80, 24}, {100, 30}}
	i := 0
	var mu sync.Mutex
	size := func() (int, int, bool) {
		mu.Lock()
		defer mu.Unlock()
		s := sizes[min(i, len(sizes)-1)]
		return s[0], s[1], true
	}
	got := make(chan wire.Frame, 8)
	go func() {
		r := wire.NewReader(b)
		for {
			f, err := r.Next()
			if err != nil {
				close(got)
				return
			}
			got <- f
		}
	}()
	in, inW := io.Pipe()
	done := make(chan Result, 1)
	go func() {
		done <- Loop(a, Options{In: in, Out: io.Discard, Size: size, DetachKey: DefaultDetachKey, Poll: 20 * time.Millisecond})
	}()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	i = 1
	mu.Unlock()
	deadline := time.After(3 * time.Second)
	for sawResize := false; !sawResize; {
		select {
		case f := <-got:
			if f.Type == wire.Resize {
				var m wire.ResizeMsg
				_ = f.JSON(&m)
				sawResize = m.Cols == 100 && m.Rows == 30
			}
		case <-deadline:
			t.Fatal("the new terminal size was never sent")
		}
	}
	_ = inW.Close() // input ended
	select {
	case res := <-done:
		if res.Exit != nil || res.Err != nil {
			t.Fatalf("result %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attach did not return when its input ended")
	}
}

// fakeConnPair returns a host.Conn wired to a raw connection for the other side.
func fakeConnPair(t *testing.T) (*host.Conn, io.ReadWriteCloser) {
	t.Helper()
	c1, c2 := net.Pipe()
	t.Cleanup(func() { c1.Close(); c2.Close() })
	return &host.Conn{Conn: c1, R: wire.NewReader(c1), W: wire.NewWriter(c1)}, c2
}

func TestParseDetachKey(t *testing.T) {
	cases := map[string]byte{
		"ctrl+]": 0x1d, "Ctrl+]": 0x1d, "ctrl+a": 0x01, "ctrl+z": 0x1a, "ctrl+\\": 0x1c, "": 0x1d, "nonsense": 0x1d, "ctrl+1": 0x1d,
	}
	for in, want := range cases {
		if got := ParseDetachKey(in); got != want {
			t.Errorf("ParseDetachKey(%q) = %#x, want %#x", in, got, want)
		}
	}
}

func TestModeFilterRemovesWin32InputModeSwitches(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"enable", []string{"a\x1b[?9001hb"}, "ab"},
		{"disable", []string{"a\x1b[?9001lb"}, "ab"},
		{"split in the middle", []string{"a\x1b[?90", "01hb"}, "ab"},
		{"split after escape", []string{"a\x1b", "[?9001lb"}, "ab"},
		{"other modes pass through", []string{"\x1b[?25l\x1b[?1004h\x1b[2J"}, "\x1b[?25l\x1b[?1004h\x1b[2J"},
		{"a lookalike is kept", []string{"\x1b[?9002h", "\x1b[?90x"}, "\x1b[?9002h\x1b[?90x"},
		{"escape at the end is kept once complete", []string{"x\x1b", "y"}, "x\x1by"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			f := &modeFilter{w: &buf}
			for _, ch := range c.chunks {
				if n, err := f.Write([]byte(ch)); err != nil || n != len(ch) {
					t.Fatalf("write: %d %v", n, err)
				}
			}
			if buf.String() != c.want {
				t.Fatalf("got %q, want %q", buf.String(), c.want)
			}
		})
	}
}
