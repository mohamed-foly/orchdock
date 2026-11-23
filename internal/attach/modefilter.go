package attach

import (
	"bytes"
	"io"
)

// win32InputMode switches a terminal to "win32 input mode", in which it sends
// every key as a long escape sequence. A program in a run asks for it when it
// starts, but the request is meant for that program's own console. Forwarded
// to the terminal attach runs in, it would change how that terminal encodes the
// keys the user types (the detach key would arrive as a sequence, not as a
// byte). Attach keeps the user's terminal in charge of its own input mode.
var win32InputMode = [][]byte{[]byte("\x1b[?9001h"), []byte("\x1b[?9001l")}

// modeFilter removes those switches from the output passed through to w. It
// keeps state between writes, so a sequence split across two chunks is removed
// too.
type modeFilter struct {
	w       io.Writer
	pending []byte // the start of a possible sequence at the end of the last chunk
}

func (f *modeFilter) Write(p []byte) (int, error) {
	data := append(f.pending, p...)
	f.pending = nil
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] != 0x1b {
			out = append(out, data[i])
			i++
			continue
		}
		rest := data[i:]
		if seq, ok := matchPrefix(rest, win32InputMode); ok {
			i += len(seq) // drop it
			continue
		}
		if couldBecome(rest, win32InputMode) {
			f.pending = append([]byte(nil), rest...) // wait for the next chunk
			break
		}
		out = append(out, data[i])
		i++
	}
	if len(out) > 0 {
		if _, err := f.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// matchPrefix reports which of seqs rest starts with.
func matchPrefix(rest []byte, seqs [][]byte) ([]byte, bool) {
	for _, s := range seqs {
		if bytes.HasPrefix(rest, s) {
			return s, true
		}
	}
	return nil, false
}

// couldBecome reports whether rest is a proper start of one of seqs.
func couldBecome(rest []byte, seqs [][]byte) bool {
	for _, s := range seqs {
		if len(rest) < len(s) && bytes.HasPrefix(s, rest) {
			return true
		}
	}
	return false
}
