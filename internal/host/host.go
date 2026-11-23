package host

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	pty "github.com/aymanbagabas/go-pty"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/fsutil"
	"github.com/mohamed-foly/orchdock/internal/proctree"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

const (
	maxViewers     = 16
	helloTimeout   = 5 * time.Second
	writeTimeout   = 2 * time.Second
	drainTimeout   = 300 * time.Millisecond
	bellMinGap     = time.Second
	nudgeDelay     = 50 * time.Millisecond
	stopGrace      = 5 * time.Second // stop: wait after Ctrl+C
	stopAgentGap   = 400 * time.Millisecond
	stopAgentGrace = 8 * time.Second
	stopCustomGap  = 500 * time.Millisecond
	terminateGrace = 3 * time.Second
)

type viewer struct {
	conn   net.Conn
	w      *wire.Writer
	client string
	// quiet connections (status pings, the service's monitor) get control
	// frames such as attention and exit, but never the screen: no replay, no
	// output, no resize nudge, and they never become the active viewer.
	quiet bool
}

// isViewer reports whether a client kind shows the terminal to a person.
func isViewer(client string) bool { return client == wire.ClientCLI || client == wire.ClientUI }

func (v *viewer) send(t wire.Type, payload []byte) error {
	_ = v.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return v.w.WriteFrame(t, payload)
}

func (v *viewer) sendJSON(t wire.Type, msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return v.send(t, b)
}

func (v *viewer) sendData(t wire.Type, data []byte) error {
	_ = v.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return v.w.WriteData(t, data)
}

type host struct {
	dir  string
	spec domain.RunSpec
	tok  string
	logf *log.Logger

	p    pty.Pty
	cmd  *pty.Cmd
	tree proctree.Tree
	ring *ring
	out  *capLog

	// mu guards viewers and orders output: replay and live output never
	// overlap or leave a gap because both happen while it is held.
	mu      sync.Mutex
	viewers map[*viewer]struct{}
	cols    int
	rows    int

	active atomic.Pointer[viewer] // last viewer that attached or sent input

	// pump-goroutine only.
	bell     bellScanner
	lastBell time.Time

	bytesOut atomic.Int64
	lastOut  atomic.Int64 // unix nanoseconds
	alive    atomic.Bool
	killed   atomic.Bool // a stop or kill was requested
	stopping atomic.Bool
	waiting  atomic.Bool // child exited, keepOpenOnExit holds the host open

	done      chan struct{} // closed when the child has exited
	closeCh   chan struct{} // closed to release a host held open
	closeOnce sync.Once
}

// Run is the host: it reads dir/spec.json, runs the command and serves viewers
// until the command ends (and, with keepOpenOnExit, the user closes the
// terminal). It returns when the host should exit.
func Run(dir string) error {
	spec, err := ReadSpec(dir)
	if err != nil {
		return err
	}
	debug.SetMemoryLimit(24 << 20)
	debug.SetGCPercent(50)

	lf, err := os.OpenFile(filepath.Join(dir, HostLogFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open host log: %w", err)
	}
	defer lf.Close()
	h := &host{
		dir:     dir,
		spec:    spec,
		logf:    log.New(lf, "", log.LstdFlags|log.Lmicroseconds),
		viewers: map[*viewer]struct{}{},
		cols:    spec.Cols,
		rows:    spec.Rows,
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}
	scroll := spec.ScrollbackBytes
	if scroll == 0 {
		scroll = DefaultScrollbackBytes
	}
	logMax := int64(spec.LogMaxBytes)
	if logMax == 0 {
		logMax = DefaultLogMaxBytes
	}
	h.ring = newRing(scroll)
	if h.out, err = openCapLog(filepath.Join(dir, LogFile), logMax); err != nil {
		return fmt.Errorf("open output log: %w", err)
	}
	defer h.out.Close()

	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	h.tok = hex.EncodeToString(tok)

	inJob := selfInJob()
	h.logf.Printf("host pid=%d inJob=%v argv=%q cwd=%q", os.Getpid(), inJob, spec.Argv, spec.Cwd)
	if err := prepareProcess(); err != nil {
		h.logf.Printf("prepare process: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	if h.p, err = pty.New(); err != nil {
		return fmt.Errorf("create pty: %w", err)
	}
	if err := h.p.Resize(spec.Cols, spec.Rows); err != nil {
		h.logf.Printf("initial resize: %v", err)
	}
	if h.cmd, h.tree, err = startChild(h.p, spec); err != nil {
		go h.p.Close()
		return err
	}
	h.alive.Store(true)

	info := Info{
		PID: os.Getpid(), ChildPID: h.cmd.Process.Pid, Port: ln.Addr().(*net.TCPAddr).Port,
		Token: h.tok, StartedAt: time.Now().UTC(), InJob: inJob,
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, InfoFile), b, 0o600); err != nil {
		_ = h.tree.Kill()
		_ = h.tree.Close()
		go h.p.Close()
		return fmt.Errorf("write %s: %w", InfoFile, err)
	}

	pumpDone := make(chan struct{})
	go func() { defer close(pumpDone); h.pump() }()
	go h.accept(ln)

	waitErr := h.cmd.Wait()
	exitCode := -1
	if h.cmd.ProcessState != nil {
		exitCode = h.cmd.ProcessState.ExitCode()
	} else if waitErr != nil {
		h.logf.Printf("wait: %v", waitErr)
	}
	h.alive.Store(false)
	close(h.done)
	h.logf.Printf("child exited code=%d", exitCode)

	select { // let the last output reach the log and the viewers
	case <-pumpDone:
	case <-time.After(drainTimeout):
	}

	reason := ReasonExited
	if h.killed.Load() {
		reason = ReasonKilled
	}
	rb, _ := json.Marshal(Result{ExitCode: exitCode, EndedAt: time.Now().UTC(), Reason: reason})
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, ResultFile), rb, 0o600); err != nil {
		h.logf.Printf("write %s: %v", ResultFile, err)
	}

	if spec.KeepOpenOnExit {
		h.emit([]byte(fmt.Sprintf("\r\n[process exited with code %d - press Enter to close]\r\n", exitCode)))
		h.waiting.Store(true)
		<-h.closeCh
	}

	exitFrame, _ := json.Marshal(wire.ExitMsg{ExitCode: exitCode, Reason: reason})
	h.mu.Lock()
	for v := range h.viewers {
		_ = v.send(wire.Exit, exitFrame)
		_ = v.conn.Close()
	}
	h.viewers = map[*viewer]struct{}{}
	h.mu.Unlock()

	_ = os.Remove(filepath.Join(dir, InfoFile))
	_ = h.tree.Close() // kills leftovers so a finished run never leaves orphans
	go h.p.Close()     // closing a pseudo-console can block; never hold up exit
	return nil
}

// pump reads PTY output until it closes. It runs even with no viewer attached:
// a PTY nobody reads eventually blocks the command.
func (h *host) pump() {
	buf := make([]byte, 32<<10)
	for {
		n, err := h.p.Read(buf)
		if n > 0 {
			h.emit(buf[:n])
			if h.bell.Feed(buf[:n]) && time.Since(h.lastBell) >= bellMinGap {
				h.lastBell = time.Now()
				h.broadcastJSON(wire.Attention, wire.AttentionMsg{Reason: "bell"})
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				h.logf.Printf("pty read: %v", err)
			}
			return
		}
	}
}

// emit records output and sends it to every viewer.
func (h *host) emit(chunk []byte) {
	h.bytesOut.Add(int64(len(chunk)))
	h.lastOut.Store(time.Now().UnixNano())
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ring.Write(chunk)
	_, _ = h.out.Write(chunk)
	for v := range h.viewers {
		if v.quiet {
			continue
		}
		if v.sendData(wire.Output, chunk) != nil {
			_ = v.conn.Close()
			delete(h.viewers, v)
		}
	}
}

func (h *host) broadcastJSON(t wire.Type, msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for v := range h.viewers {
		if v.send(t, b) != nil {
			_ = v.conn.Close()
			delete(h.viewers, v)
		}
	}
}

func (h *host) accept(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go h.serve(c)
	}
}

func (h *host) serve(c net.Conn) {
	defer c.Close()
	r := wire.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(helloTimeout))
	f, err := r.Next()
	if err != nil || f.Type != wire.Hello {
		return
	}
	var hello wire.HelloMsg
	if f.JSON(&hello) != nil || subtle.ConstantTimeCompare([]byte(hello.Token), []byte(h.tok)) != 1 {
		h.logf.Printf("rejected hello from %v", c.RemoteAddr())
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	v := &viewer{conn: c, w: wire.NewWriter(c), client: hello.Client, quiet: !isViewer(hello.Client)}
	h.mu.Lock()
	if len(h.viewers) >= maxViewers {
		h.mu.Unlock()
		return
	}
	// Replay and registration are atomic: no output is lost or duplicated.
	cols, rows := h.cols, h.rows
	welcome := wire.WelcomeMsg{RunID: h.spec.RunID, ChildPID: h.cmd.Process.Pid, Cols: cols, Rows: rows,
		StartedAt: h.spec.CreatedAt.Format(time.RFC3339)}
	_ = v.sendJSON(wire.Welcome, welcome)
	if !v.quiet {
		_ = v.sendData(wire.Output, h.ring.Snapshot())
	}
	_ = v.send(wire.ReplayEnd, nil)
	h.viewers[v] = struct{}{}
	h.mu.Unlock()
	h.logf.Printf("viewer attached: %s %v", v.client, c.RemoteAddr())
	defer func() {
		h.mu.Lock()
		delete(h.viewers, v)
		h.mu.Unlock()
		h.active.CompareAndSwap(v, nil)
		h.logf.Printf("viewer detached: %s %v", v.client, c.RemoteAddr())
	}()

	if !v.quiet {
		h.active.Store(v)
		if hello.Cols > 0 && hello.Rows > 0 {
			h.resize(hello.Cols, hello.Rows)
		}
		if h.alive.Load() {
			go h.repaint() // TUIs redraw on resize, so the viewer gets a clean current screen
		}
	}

	for {
		f, err := r.Next()
		if err != nil {
			return
		}
		switch f.Type {
		case wire.Input:
			h.active.Store(v)
			h.input(f.Payload)
		case wire.Resize:
			var m wire.ResizeMsg
			if f.JSON(&m) == nil && h.active.Load() == v {
				h.resize(m.Cols, m.Rows)
			}
		case wire.Signal:
			var m wire.SignalMsg
			if f.JSON(&m) == nil {
				h.signal(m)
			}
		case wire.Ping:
			_ = v.send(wire.Pong, nil)
		case wire.Status:
			_ = v.sendJSON(wire.Status, h.status())
		}
	}
}

func (h *host) status() wire.StatusMsg {
	h.mu.Lock()
	n := len(h.viewers)
	h.mu.Unlock()
	s := wire.StatusMsg{ChildPID: h.cmd.Process.Pid, Alive: h.alive.Load(), Viewers: n, BytesOut: h.bytesOut.Load()}
	if t := h.lastOut.Load(); t != 0 {
		s.LastOutputAt = time.Unix(0, t).UTC().Format(time.RFC3339)
	}
	return s
}

func (h *host) input(p []byte) {
	if h.waiting.Load() {
		for _, b := range p {
			if b == '\r' || b == '\n' {
				h.release()
				return
			}
		}
		return
	}
	if _, err := h.p.Write(p); err != nil && h.alive.Load() {
		h.logf.Printf("write input: %v", err)
	}
}

func (h *host) release() { h.closeOnce.Do(func() { close(h.closeCh) }) }

func (h *host) resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	h.mu.Lock()
	h.cols, h.rows = cols, rows
	h.mu.Unlock()
	if err := h.p.Resize(cols, rows); err != nil {
		h.logf.Printf("resize: %v", err)
	}
}

// repaint nudges the terminal size by one column and back.
func (h *host) repaint() {
	h.mu.Lock()
	cols, rows := h.cols, h.rows
	h.mu.Unlock()
	if cols < 2 || h.p.Resize(cols-1, rows) != nil {
		return
	}
	time.Sleep(nudgeDelay)
	_ = h.p.Resize(cols, rows)
}

// signal handles a viewer's request to interrupt or stop the command.
func (h *host) signal(m wire.SignalMsg) {
	h.logf.Printf("signal %s", m.Signal)
	if h.waiting.Load() { // the command is gone; any stop request closes the held terminal
		h.release()
		return
	}
	if !h.alive.Load() {
		return
	}
	switch m.Signal {
	case wire.SigInterrupt:
		_, _ = h.p.Write([]byte{0x03})
	case wire.SigKill:
		h.killed.Store(true)
		_ = h.tree.Kill()
	case wire.SigStop, wire.SigStopAgent:
		h.killed.Store(true)
		if h.stopping.CompareAndSwap(false, true) {
			go func() {
				defer h.stopping.Store(false)
				h.stopSequence(m)
			}()
		}
	default:
		h.logf.Printf("unknown signal %q", m.Signal)
	}
}

// stopSequence ends the command politely, then firmly:
//
//	stop:       [custom text] → Ctrl+C → wait → terminate → wait → kill
//	stop-agent: Ctrl+C → short wait → Ctrl+C → wait → terminate → wait → kill
//
// Agent CLIs usually need two Ctrl+C presses to exit.
func (h *host) stopSequence(m wire.SignalMsg) {
	ctrlC := func() { _, _ = h.p.Write([]byte{0x03}) }
	switch m.Signal {
	case wire.SigStopAgent:
		ctrlC()
		if h.waitDone(stopAgentGap) {
			return
		}
		ctrlC()
		if h.waitDone(stopAgentGrace) {
			return
		}
	default:
		if m.Text != "" {
			_, _ = h.p.Write([]byte(m.Text))
			if h.waitDone(stopCustomGap) {
				return
			}
		}
		ctrlC()
		if h.waitDone(stopGrace) {
			return
		}
	}
	_ = h.tree.Terminate()
	if h.waitDone(terminateGrace) {
		return
	}
	_ = h.tree.Kill()
}

// waitDone waits up to d for the command to exit and reports whether it did.
func (h *host) waitDone(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-h.done:
		return true
	case <-t.C:
		return false
	}
}
