package host

import (
	"io"
	"os"
	"time"
)

// capLog is an append-only log file with a soft size cap. When the file grows
// past max bytes it is rewritten to keep only its newest half. Trimming happens
// at most once per minGap so a flood of output costs little.
type capLog struct {
	path    string
	max     int64
	minGap  time.Duration
	now     func() time.Time
	f       *os.File
	size    int64
	lastCut time.Time
}

// openCapLog opens (or creates) the log. A max of 0 means no cap.
func openCapLog(path string, max int64) (*capLog, error) {
	l := &capLog{path: path, max: max, minGap: 5 * time.Second, now: time.Now}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *capLog) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

// Write appends p and trims the file when it is over the cap. A log that
// cannot be written never stops the run: output is dropped instead.
func (l *capLog) Write(p []byte) (int, error) {
	if l.f == nil {
		return len(p), nil
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	if err == nil && l.max > 0 && l.size > l.max && l.now().Sub(l.lastCut) >= l.minGap {
		l.trim()
	}
	return n, err
}

// trim keeps the newest half of the cap.
func (l *capLog) trim() {
	l.lastCut = l.now()
	_ = l.f.Close()
	l.f = nil
	l.rewriteTail(l.max / 2)
	if err := l.open(); err != nil {
		l.f = nil
	}
}

// rewriteTail replaces the file with its last keep bytes, through a temp file
// so that a failure leaves the original in place.
func (l *capLog) rewriteTail(keep int64) {
	src, err := os.Open(l.path)
	if err != nil {
		return
	}
	defer src.Close()
	if _, err := src.Seek(-keep, io.SeekEnd); err != nil {
		return
	}
	tmp := l.path + ".tmp"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = src.Close() // Windows cannot rename over an open file
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
	}
}

// Close closes the file.
func (l *capLog) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}
