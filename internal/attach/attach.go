// Package attach shows a running run's terminal in the user's own terminal and
// sends their keys back, until they press the detach key. The run keeps going.
package attach

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/mohamed-foly/orchdock/internal/config"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

// DefaultDetachKey is Ctrl+].
const DefaultDetachKey = 0x1d

// DetachKey reads the detach key from the settings: "ctrl+<letter>" or "ctrl+]".
func DetachKey(home string) byte {
	cfg, err := config.Load(home)
	if err != nil {
		return DefaultDetachKey
	}
	return ParseDetachKey(cfg.Terminal.DetachKey)
}

// ParseDetachKey turns "ctrl+x" into its control byte; anything else is Ctrl+].
func ParseDetachKey(s string) byte {
	s = strings.ToLower(strings.TrimSpace(s))
	if k, ok := strings.CutPrefix(s, "ctrl+"); ok && len(k) == 1 {
		c := k[0]
		switch {
		case c >= 'a' && c <= 'z', c == '[', c == '\\', c == ']', c == '^', c == '_':
			return c & 0x1f
		}
	}
	return DefaultDetachKey
}

// Interactive connects the terminal (in, out) to the run behind info. When in
// is a terminal it is put in raw mode, and it is always restored. It prints a
// short message when attach ends and always leaves the run running.
func Interactive(info host.Info, alias, home string, in io.Reader, out io.Writer) error {
	opt := Options{In: in, Out: &modeFilter{w: out}, DetachKey: DetachKey(home), Poll: 250 * time.Millisecond}
	var restore func()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if state, err := term.MakeRaw(int(f.Fd())); err == nil {
			undoInput := enableVTInput(f)
			restore = func() { undoInput(); _ = term.Restore(int(f.Fd()), state) }
		}
	}
	if f, ok := out.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		undoOutput := enableVTOutput(f)
		prev := restore
		restore = func() {
			undoOutput()
			if prev != nil {
				prev()
			}
		}
		opt.Size = func() (int, int, bool) {
			w, h, err := term.GetSize(int(f.Fd()))
			return w, h, err == nil
		}
	}
	cols, rows := 120, 32
	if opt.Size != nil {
		if w, h, ok := opt.Size(); ok {
			cols, rows = w, h
		}
	}
	conn, err := host.Dial(info, wire.HelloMsg{Client: "cli", Cols: cols, Rows: rows})
	if err != nil {
		if restore != nil {
			restore()
		}
		return fmt.Errorf("%w: %v", host.ErrNoHost, err)
	}
	defer conn.Close()
	res := Loop(conn, opt)
	if restore != nil {
		restore()
	}
	switch {
	case res.Exit != nil:
		fmt.Fprintf(out, "\r\n[%s ended with code %d]\r\n", alias, res.Exit.ExitCode)
	case res.Err != nil:
		fmt.Fprintf(out, "\r\n[connection to %s lost: %v]\r\n", alias, res.Err)
	default:
		fmt.Fprintf(out, "\r\n[detached from %s - still running]\r\n", alias)
	}
	return nil
}

// Options configure Loop.
type Options struct {
	In        io.Reader
	Out       io.Writer
	Size      func() (cols, rows int, ok bool) // nil: never send resizes
	DetachKey byte
	Poll      time.Duration // how often the size is checked
}

// Result says why Loop returned.
type Result struct {
	Exit *wire.ExitMsg // the run ended
	Err  error         // the connection broke
}

// Loop shows the run on opt.Out and sends opt.In to it until the detach key
// is pressed, input ends, the run ends, or the connection breaks. Keys are
// sent as typed; nothing is sent on the user's behalf.
func Loop(conn *host.Conn, opt Options) Result {
	frames := make(chan wire.Frame)
	connErr := make(chan error, 1)
	go func() {
		for {
			f, err := conn.R.Next()
			if err != nil {
				connErr <- err
				return
			}
			frames <- f
		}
	}()
	keys := make(chan []byte)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := opt.In.Read(buf)
			if n > 0 {
				keys <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(keys)
				return
			}
		}
	}()
	var tick <-chan time.Time
	lastCols, lastRows := 0, 0
	if opt.Size != nil && opt.Poll > 0 {
		t := time.NewTicker(opt.Poll)
		defer t.Stop()
		tick = t.C
		lastCols, lastRows, _ = opt.Size()
	}
	for {
		select {
		case f := <-frames:
			switch f.Type {
			case wire.Output:
				_, _ = opt.Out.Write(f.Payload)
			case wire.Exit:
				var m wire.ExitMsg
				_ = f.JSON(&m)
				return Result{Exit: &m}
			}
		case err := <-connErr:
			if errors.Is(err, io.EOF) {
				return Result{Err: errors.New("the host closed the connection")}
			}
			return Result{Err: err}
		case k, ok := <-keys:
			if !ok {
				return Result{} // input ended: detach
			}
			if i := indexByte(k, opt.DetachKey); i >= 0 {
				if i > 0 {
					_ = conn.W.WriteData(wire.Input, k[:i])
				}
				return Result{}
			}
			if err := conn.W.WriteData(wire.Input, k); err != nil {
				return Result{Err: err}
			}
		case <-tick:
			if w, h, ok := opt.Size(); ok && (w != lastCols || h != lastRows) {
				lastCols, lastRows = w, h
				_ = conn.W.WriteJSON(wire.Resize, wire.ResizeMsg{Cols: w, Rows: h})
			}
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
