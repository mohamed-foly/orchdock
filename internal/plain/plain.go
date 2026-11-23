// Package plain turns a terminal stream into readable text.
package plain

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IsTerminal reports whether w is an interactive terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Writer turns a terminal stream into readable text: escape sequences
// (colours, cursor movement, window titles) and control characters are dropped,
// and the runs of blank lines that screen repaints leave behind are collapsed.
// It keeps state between writes, so a sequence split across two chunks is
// still removed.
type Writer struct {
	w     io.Writer
	state state
	nl    int // consecutive newlines just written
}

type state uint8

const (
	text   state = iota
	esc          // after ESC
	csi          // inside ESC [ ... until a final byte
	osc          // inside ESC ] ... until BEL or ESC \
	oscEsc       // ESC inside an OSC string
	str          // inside DCS, SOS, PM or APC: until ESC \
	strEsc       // ESC inside one of those
)

// NewWriter returns a Writer that writes plain text to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Write implements io.Writer. It always reports len(b) bytes consumed.
func (p *Writer) Write(b []byte) (int, error) {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		switch p.state {
		case text:
			switch {
			case c == 0x1b:
				p.state = esc
			case c == '\n':
				if p.nl < 2 { // at most one blank line in a row
					out = append(out, '\n')
				}
				p.nl++
			case c == '\t' || c >= 0x20 && c != 0x7f:
				out = append(out, c)
				p.nl = 0
			}
		case esc:
			switch c {
			case '[':
				p.state = csi
			case ']':
				p.state = osc
			case 'P', 'X', '^', '_':
				p.state = str
			default:
				p.state = text // a two-character sequence such as ESC =
			}
		case csi:
			if c >= 0x40 && c <= 0x7e {
				p.state = text
			}
		case osc:
			switch c {
			case 0x07:
				p.state = text
			case 0x1b:
				p.state = oscEsc
			}
		case oscEsc:
			if c == '\\' {
				p.state = text
			} else {
				p.state = osc
			}
		case str:
			if c == 0x1b {
				p.state = strEsc
			}
		case strEsc:
			if c == '\\' {
				p.state = text
			} else {
				p.state = str
			}
		}
	}
	if len(out) > 0 {
		if _, err := p.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

// OutputWriter chooses how run output is shown: as it is on a terminal or when
// raw is requested, as plain text otherwise.
func OutputWriter(out io.Writer, raw bool) io.Writer {
	if raw || IsTerminal(out) {
		return out
	}
	return NewWriter(out)
}

// Bytes returns data as plain text.
func Bytes(data []byte) []byte {
	var buf bytesBuffer
	_, _ = NewWriter(&buf).Write(data)
	return buf.b
}

type bytesBuffer struct{ b []byte }

func (b *bytesBuffer) Write(p []byte) (int, error) { b.b = append(b.b, p...); return len(p), nil }
