package plain

import (
	"bytes"
	"testing"
)

func TestPlainWriter(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"colours", []string{"\x1b[31mred\x1b[0m text"}, "red text"},
		{"cursor and erase", []string{"\x1b[H\x1b[Khello\x1b[K\r\n"}, "hello\n"},
		{"window title with BEL", []string{"\x1b]0;a title\aafter"}, "after"},
		{"window title with ST", []string{"\x1b]0;a title\x1b\\after"}, "after"},
		{"split sequence", []string{"ab\x1b[3", "1mcd", "\x1b]0;ti", "tle\aef"}, "abcdef"},
		{"private modes", []string{"\x1b[?25l\x1b[?9001hx\x1b[?25h"}, "x"},
		{"blank line runs collapse", []string{"a\n\n\n\n\nb\n"}, "a\n\nb\n"},
		{"carriage returns and bells are dropped", []string{"a\r\nb\a\r\n"}, "a\nb\n"},
		{"tabs stay", []string{"a\tb"}, "a\tb"},
		{"dcs string", []string{"x\x1bPq\x07data\x1b\\y"}, "xy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			for _, ch := range c.chunks {
				if n, err := w.Write([]byte(ch)); err != nil || n != len(ch) {
					t.Fatalf("write: %d %v", n, err)
				}
			}
			if buf.String() != c.want {
				t.Fatalf("got %q, want %q", buf.String(), c.want)
			}
		})
	}
}
