package host

import "sync"

// ring is a fixed-size circular buffer of the most recent terminal output.
type ring struct {
	mu      sync.Mutex
	buf     []byte
	pos     int
	wrapped bool
}

func newRing(size int) *ring {
	if size < 1 {
		size = 1
	}
	return &ring{buf: make([]byte, size)}
}

// Write appends p, overwriting the oldest bytes when full.
func (r *ring) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(p) >= len(r.buf) { // only the tail can survive
		copy(r.buf, p[len(p)-len(r.buf):])
		r.pos, r.wrapped = 0, true
		return
	}
	for len(p) > 0 {
		n := copy(r.buf[r.pos:], p)
		p = p[n:]
		r.pos += n
		if r.pos == len(r.buf) {
			r.pos, r.wrapped = 0, true
		}
	}
}

// Snapshot returns the buffered output, oldest first. Once the buffer has
// wrapped, the snapshot starts after the first newline so that a replay never
// begins in the middle of an escape sequence or a line.
func (r *ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.wrapped {
		return append([]byte(nil), r.buf[:r.pos]...)
	}
	out := make([]byte, 0, len(r.buf))
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	for i, b := range out {
		if b == '\n' {
			return out[i+1:]
		}
	}
	return out
}
