package host

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/procs"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

func TestEchoAttachDetachReplay(t *testing.T) {
	r := startRun(t, "echo", "", nil)

	a := r.dial("cli")
	readUntil(t, a, "ready", 5*time.Second)
	if err := a.W.WriteFrame(wire.Input, []byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, a, "echo:hello", 5*time.Second)
	a.Close() // detach: the run must keep going

	time.Sleep(200 * time.Millisecond)
	if !Alive(r.dir) {
		t.Fatal("host must survive a viewer detaching")
	}
	b := r.dial("ui")
	got := replay(t, b)
	if !strings.Contains(got, "ready") || !strings.Contains(got, "echo:hello") {
		t.Fatalf("late viewer must get the scrollback, got %q", got)
	}
	if err := b.W.WriteFrame(wire.Input, []byte("again\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, b, "echo:again", 5*time.Second)
}

func TestTwoViewersSeeTheSameOutput(t *testing.T) {
	r := startRun(t, "echo", "", nil)
	a, b := r.dial("cli"), r.dial("ui")
	readUntil(t, a, "ready", 5*time.Second)
	readUntil(t, b, "ready", 5*time.Second)
	_ = a.W.WriteFrame(wire.Input, []byte("shared\r"))
	readUntil(t, a, "echo:shared", 5*time.Second)
	readUntil(t, b, "echo:shared", 5*time.Second)
}

func TestWrongTokenIsRejected(t *testing.T) {
	r := startRun(t, "sleep", "", nil)
	bad := r.info
	bad.Token = strings.Repeat("0", 64)
	if c, err := Dial(bad, wire.HelloMsg{Client: "cli"}); err == nil {
		c.Close()
		t.Fatal("a wrong token must be refused")
	}
	if !Alive(r.dir) {
		t.Fatal("a rejected client must not disturb the host")
	}
}

func TestInfoFilePermissionsAndContent(t *testing.T) {
	r := startRun(t, "sleep", "", nil)
	st, err := os.Stat(filepath.Join(r.dir, InfoFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("host.json mode %v", st.Mode().Perm())
	}
	if r.info.Token == "" || r.info.Port == 0 || r.info.ChildPID == 0 || r.info.PID == 0 {
		t.Fatalf("incomplete info %+v", r.info)
	}
}

func TestExitCodeIsRecorded(t *testing.T) {
	r := startRun(t, "exit", "3", nil)
	res, ok := r.waitExit(10 * time.Second)
	if !ok {
		t.Fatal("host did not finish")
	}
	if res.ExitCode != 3 || res.Reason != ReasonExited {
		t.Fatalf("result %+v", res)
	}
	if _, err := os.Stat(filepath.Join(r.dir, InfoFile)); !os.IsNotExist(err) {
		t.Fatal("host.json must be removed when the run ends")
	}
	log, _ := os.ReadFile(filepath.Join(r.dir, LogFile))
	if !strings.Contains(string(log), "bye") {
		t.Fatalf("output.log %q", log)
	}
}

func TestExitFrameReachesViewer(t *testing.T) {
	r := startRun(t, "echo", "", nil)
	c := r.dial("cli")
	readUntil(t, c, "ready", 5*time.Second)
	if err := SendSignal(r.dir, wire.SignalMsg{Signal: wire.SigKill}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("no EXIT frame before the connection closed: %v", err)
		}
		if f.Type == wire.Exit {
			var m wire.ExitMsg
			if err := f.JSON(&m); err != nil || m.Reason != ReasonKilled {
				t.Fatalf("exit %+v %v", m, err)
			}
			return
		}
	}
}

func TestOutputIsDrainedWithoutViewers(t *testing.T) {
	// 3 MiB with nobody attached: an unread PTY would block the command.
	r := startRun(t, "spam", strconv.Itoa(3<<20), func(s *domain.RunSpec) {
		s.ScrollbackBytes = 64 << 10
		s.LogMaxBytes = 1 << 30
	})
	res, ok := r.waitExit(30 * time.Second)
	if !ok || res.ExitCode != 0 {
		t.Fatalf("run did not finish cleanly: %+v ok=%v", res, ok)
	}
	log, err := os.ReadFile(filepath.Join(r.dir, LogFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(log) < 3<<20 || !strings.Contains(string(log), "SPAM-DONE") {
		t.Fatalf("log has %d bytes", len(log))
	}
}

func TestScrollbackIsBounded(t *testing.T) {
	r := startRun(t, "spam", strconv.Itoa(2<<20), func(s *domain.RunSpec) {
		s.ScrollbackBytes = 32 << 10
		s.KeepOpenOnExit = true // keep the host alive so the replay can be read
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := ReadResult(r.dir); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	c := r.dial("ui")
	got := replay(t, c)
	if len(got) > 32<<10+4096 { // plus the "process exited" notice
		t.Fatalf("replay is %d bytes, want at most the 32 KiB scrollback", len(got))
	}
	if !strings.Contains(got, "SPAM-DONE") {
		t.Fatal("replay must end with the newest output")
	}
	if !strings.HasPrefix(got, "line ") {
		t.Fatalf("replay must start on a line boundary, got %q", got[:20])
	}
}

func TestKeepOpenOnExit(t *testing.T) {
	r := startRun(t, "exit", "0", func(s *domain.RunSpec) { s.KeepOpenOnExit = true })
	c := r.dial("cli")
	readUntil(t, c, "press Enter to close", 10*time.Second)
	if res, err := ReadResult(r.dir); err != nil || res.ExitCode != 0 {
		t.Fatalf("result must be written when the command ends: %+v %v", res, err)
	}
	if !Alive(r.dir) {
		t.Fatal("host must stay until Enter")
	}
	if err := c.W.WriteFrame(wire.Input, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.waitExit(10 * time.Second); !ok {
		t.Fatal("Enter must release the host")
	}
}

func TestStopSequenceInterruptsThenTerminates(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about 5 seconds")
	}
	r := startRun(t, "trap", "", nil)
	c := r.dial("cli")
	readUntil(t, c, "trapping", 5*time.Second)
	start := time.Now()
	if err := SendSignal(r.dir, wire.SignalMsg{Signal: wire.SigStop}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "got interrupt 1", 5*time.Second) // Ctrl+C reached the command
	res, ok := r.waitExit(20 * time.Second)
	if !ok {
		t.Fatal("stop did not end the run")
	}
	if res.Reason != ReasonKilled {
		t.Fatalf("reason %q", res.Reason)
	}
	if d := time.Since(start); d < 4*time.Second {
		t.Fatalf("a command that ignores one Ctrl+C must be given the grace period first (ended after %v)", d)
	}
}

func TestStopAgentSendsTwoInterrupts(t *testing.T) {
	r := startRun(t, "trap", "", nil)
	c := r.dial("cli")
	readUntil(t, c, "trapping", 5*time.Second)
	start := time.Now()
	if err := SendSignal(r.dir, wire.SignalMsg{Signal: wire.SigStopAgent}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "got interrupt 2", 5*time.Second)
	if _, ok := r.waitExit(10 * time.Second); !ok {
		t.Fatal("run did not end")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("the second Ctrl+C must come quickly, without waiting for the grace period")
	}
}

func TestKillEndsTheWholeTree(t *testing.T) {
	r := startRun(t, "tree", "", nil)
	c := r.dial("cli")
	pid := grandchildPID(t, c)
	if !procs.Alive(pid) || !procs.Alive(r.info.ChildPID) {
		t.Fatalf("both processes should be alive before the kill (child %d, grandchild %d)", r.info.ChildPID, pid)
	}
	if err := SendSignal(r.dir, wire.SignalMsg{Signal: wire.SigKill}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.waitExit(10 * time.Second); !ok {
		t.Fatal("run did not end")
	}
	waitDead(t, pid)
	waitDead(t, r.info.ChildPID)
}

func TestKillingTheHostKillsTheTree(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("kill-on-close needs a Windows Job Object")
	}
	r := startRun(t, "tree", "", nil)
	c := r.dial("cli")
	pid := grandchildPID(t, c)
	p, err := os.FindProcess(r.info.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	waitDead(t, pid)
	waitDead(t, r.info.ChildPID)
}

func TestBellRaisesAttentionButTitleUpdateDoesNot(t *testing.T) {
	r := startRun(t, "bell", "", nil)
	c := r.dial("cli")
	attention := 0
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	sawDing := false
	for !sawDing || attention == 0 {
		f, err := c.R.Next()
		if err != nil {
			break
		}
		switch f.Type {
		case wire.Output:
			if strings.Contains(string(f.Payload), "ding") {
				sawDing = true
			}
		case wire.Attention:
			attention++
		}
		if sawDing && attention > 0 {
			break
		}
	}
	if attention != 1 {
		t.Fatalf("want exactly one attention frame (the real bell), got %d", attention)
	}
}

func TestStatusReportsChildAndViewers(t *testing.T) {
	r := startRun(t, "echo", "", nil)
	c := r.dial("cli")
	readUntil(t, c, "ready", 5*time.Second)
	if err := c.W.WriteFrame(wire.Status, nil); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatal(err)
		}
		if f.Type != wire.Status {
			continue
		}
		var s wire.StatusMsg
		if err := f.JSON(&s); err != nil {
			t.Fatal(err)
		}
		if !s.Alive || s.ChildPID != r.info.ChildPID || s.Viewers < 1 || s.BytesOut == 0 {
			t.Fatalf("status %+v", s)
		}
		return
	}
}

func TestSpecIsValidatedBeforeStarting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SpecFile), []byte(`{"argv":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(dir); err == nil {
		t.Fatal("an invalid spec must be refused")
	}
	if err := Run(t.TempDir()); err == nil {
		t.Fatal("a missing spec must be refused")
	}
}

func TestMissingExecutableFailsLaunchWithReason(t *testing.T) {
	dir := t.TempDir()
	spec := `{"runId":"r_0123456789","itemId":"_adhoc","trigger":"manual","argv":["definitely-not-a-real-program-xyz"],"cwd":` +
		strconv.Quote(dir) + `,"cols":80,"rows":24}`
	if err := os.WriteFile(filepath.Join(dir, SpecFile), []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Launch(LaunchOptions{
		Exe: os.Args[0], RunDir: dir, Timeout: 10 * time.Second,
		Env: append(os.Environ(), envRole+"=host", envDir+"="+dir),
	})
	if err == nil {
		t.Fatal("launch must fail")
	}
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !procs.Alive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}

func TestSpecWithByteOrderMarkIsAccepted(t *testing.T) {
	dir := t.TempDir()
	body := `{"runId":"r_0123456789","itemId":"_adhoc","trigger":"manual","argv":["x"],"cwd":"/","cols":80,"rows":24}`
	spec := append(append([]byte{}, utf8BOM...), body...)
	if err := os.WriteFile(filepath.Join(dir, SpecFile), spec, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSpec(dir); err != nil {
		t.Fatalf("a BOM must not break reading: %v", err)
	}
}

func TestAttachNudgesTheTerminalSoTuisRepaint(t *testing.T) {
	r := startRun(t, "size", "", nil)
	c := r.dial("ui")
	// Attaching resizes the terminal one column narrower and back, which makes
	// full-screen programs redraw. The command sees both sizes.
	var seen strings.Builder
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	narrowThenRestored := func() bool {
		s := seen.String()
		narrow := strings.Index(s, "size:99x30;")
		return narrow >= 0 && strings.LastIndex(s, "size:100x30;") > narrow
	}
	for !narrowThenRestored() {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("no nudge seen: %v (got %q)", err, seen.String())
		}
		if f.Type == wire.Output {
			seen.Write(f.Payload)
		}
	}
}

func TestStatusPingsDoNotDisturbTheTerminal(t *testing.T) {
	// A service that polls hosts must not make programs redraw: the command
	// reports every size change it sees, and pings must cause none.
	r := startRun(t, "size", "", nil)
	logPath := filepath.Join(r.dir, LogFile)
	waitUntil(t, "the command's first size report", 10*time.Second, func() bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), "size:100x30;")
	})
	for i := 0; i < 8; i++ {
		if err := Ping(r.info); err != nil {
			t.Fatal(err)
		}
	}
	if err := SendSignal(r.dir, wire.SignalMsg{Signal: wire.SigInterrupt}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	b, _ := os.ReadFile(logPath)
	if strings.Contains(string(b), "size:99x30;") {
		t.Fatal("a ping or signal resized the terminal, which makes full-screen programs redraw")
	}
}

func TestMonitorGetsAttentionButNoScreen(t *testing.T) {
	r := startRun(t, "bell", "", nil)
	c, err := Dial(r.info, wire.HelloMsg{Client: wire.ClientMonitor})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		f, err := c.R.Next()
		if err != nil {
			t.Fatalf("no attention frame: %v", err)
		}
		switch f.Type {
		case wire.Output:
			t.Fatalf("a monitor must not receive screen output, got %q", f.Payload)
		case wire.Attention:
			return
		}
	}
}

func waitUntil(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
