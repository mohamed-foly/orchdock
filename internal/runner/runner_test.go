package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/config"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/procs"
	"github.com/mohamed-foly/orchdock/internal/testchild"
)

func TestStartRecordsTheRunAndStopEndsIt(t *testing.T) {
	r := newRunner(t)
	it := addItem(t, r, domain.KindShell, "echo-box", "echo", "", nil)

	run, err := r.Start("echo-box", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != StateRunning || run.ItemID != it.ID {
		t.Fatalf("run %+v", run)
	}
	spec, err := host.ReadSpec(run.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Argv[0] != os.Args[0] || spec.Trigger != domain.TriggerManual || spec.Cols != 120 || spec.Rows != 32 {
		t.Fatalf("spec %+v", spec)
	}
	hasRole := false
	for _, kv := range spec.Env {
		hasRole = hasRole || kv == testchild.EnvRole+"=child"
	}
	if !hasRole {
		t.Fatal("the item's environment must reach the command")
	}
	output(t, run, "ready")
	if got, ok := r.Active(it.ID); !ok || got.ID != run.ID {
		t.Fatal("the run must be active")
	}
	if len(r.readDesired()) != 1 {
		t.Fatalf("desired %v", r.readDesired())
	}
	if r.item(t, "echo-box").LastRunAt.IsZero() {
		t.Fatal("lastRunAt must be recorded")
	}

	ended, err := r.Stop("echo-box", StopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ended.State != StateEnded || ended.Result == nil || ended.Result.Reason != host.ReasonKilled {
		t.Fatalf("ended %+v", ended)
	}
	if len(r.readDesired()) != 0 {
		t.Fatal("a user stop must clear the desired entry")
	}
	lines, _ := r.History(it.ID)
	if len(lines) != 1 || lines[0].Outcome != domain.OutcomeKilled || lines[0].RunID != run.ID || lines[0].Variant != "default" {
		t.Fatalf("history %+v", lines)
	}
	r.Reconcile(it.ID)
	r.Reconcile(it.ID)
	if again, _ := r.History(it.ID); len(again) != 1 {
		t.Fatalf("a run must be recorded exactly once, got %d lines", len(again))
	}
	if err := lines[0].Validate(); err != nil {
		t.Fatalf("history line must be valid: %v", err)
	}
}

func TestStartingARunningItemIsRefused(t *testing.T) {
	r := newRunner(t)
	addItem(t, r, domain.KindService, "svc", "sleep", "", nil)
	if _, err := r.Start("svc", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start("svc", StartOptions{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentRunLimit(t *testing.T) {
	r := newRunner(t)
	r.Config = func() config.Config { c := config.Default(); c.MaxConcurrentRuns = 1; return c }
	addItem(t, r, domain.KindShell, "one", "sleep", "", nil)
	addItem(t, r, domain.KindShell, "two", "sleep", "", nil)
	if _, err := r.Start("one", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start("two", StartOptions{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestMissingExecutableIsAClearError(t *testing.T) {
	r := newRunner(t)
	it := addItem(t, r, domain.KindTask, "ghost", "sleep", "", func(it *domain.Item) {
		it.Command.Argv = []string{"definitely-not-a-real-program-xyz"}
	})
	_, err := r.Start("ghost", StartOptions{})
	if !errors.Is(err, domain.ErrNotFound) || !strings.Contains(err.Error(), "definitely-not-a-real-program-xyz") {
		t.Fatalf("got %v", err)
	}
	lines, _ := r.History(it.ID)
	if len(lines) != 1 || lines[0].Outcome != domain.OutcomeStartFailed {
		t.Fatalf("a failed start must be recorded: %+v", lines)
	}
	if err := lines[0].Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMissingWorkingFolderIsRefused(t *testing.T) {
	r := newRunner(t)
	addItem(t, r, domain.KindShell, "lost", "sleep", "", func(it *domain.Item) {
		it.Command.Cwd = filepath.Join(t.TempDir(), "gone")
	})
	if _, err := r.Start("lost", StartOptions{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestStartFailureCarriesTheHostLog(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs a file that passes the PATH lookup but cannot be executed")
	}
	r := newRunner(t)
	bogus := filepath.Join(t.TempDir(), "bogus.exe")
	if err := os.WriteFile(bogus, []byte("this is not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	it := addItem(t, r, domain.KindShell, "bogus", "sleep", "", func(it *domain.Item) { it.Command.Argv = []string{bogus} })
	_, err := r.Start("bogus", StartOptions{})
	if !errors.Is(err, domain.ErrStartFailed) || !strings.Contains(err.Error(), "host log") {
		t.Fatalf("the error must include the end of the host log, got %v", err)
	}
	if lines, _ := r.History(it.ID); len(lines) != 1 || lines[0].Outcome != domain.OutcomeStartFailed {
		t.Fatalf("history %+v", lines)
	}
}

func TestUnknownHostBinaryIsStartFailed(t *testing.T) {
	r := newRunner(t)
	r.Exe = filepath.Join(t.TempDir(), "no-such-binary")
	addItem(t, r, domain.KindShell, "x", "sleep", "", nil)
	if _, err := r.Start("x", StartOptions{}); !errors.Is(err, domain.ErrStartFailed) {
		t.Fatalf("got %v", err)
	}
}

func TestFailedExitIsRecordedWithItsCode(t *testing.T) {
	r := newRunner(t)
	it := addItem(t, r, domain.KindTask, "fails", "exit", "3", nil)
	run, err := r.Start("fails", StartOptions{Trigger: domain.TriggerSchedule})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.readDesired()) != 0 {
		t.Fatal("scheduled runs are not restored after a restart")
	}
	ended, err := r.Wait(run, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ended.Result.ExitCode != 3 {
		t.Fatalf("exit code %d", ended.Result.ExitCode)
	}
	r.Reconcile(it.ID)
	lines, _ := r.History(it.ID)
	if len(lines) != 1 || lines[0].Outcome != domain.OutcomeFailed || lines[0].ExitCode == nil || *lines[0].ExitCode != 3 ||
		lines[0].Trigger != domain.TriggerSchedule {
		t.Fatalf("history %+v", lines)
	}
}

func TestSuccessfulExitIsOKAndForgotten(t *testing.T) {
	r := newRunner(t)
	it := addItem(t, r, domain.KindTask, "ok", "exit", "0", nil)
	run, err := r.Start("ok", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Wait(run, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	r.Reconcile(it.ID)
	lines, _ := r.History(it.ID)
	if len(lines) != 1 || lines[0].Outcome != domain.OutcomeOK {
		t.Fatalf("history %+v", lines)
	}
	if len(r.readDesired()) != 0 || len(r.Interrupted()) != 0 {
		t.Fatal("a run that ended by itself is not an interruption")
	}
}

func TestForceStopEndsTheWholeTreeAtOnce(t *testing.T) {
	r := newRunner(t)
	addItem(t, r, domain.KindService, "tree", "tree", "", nil)
	run, err := r.Start("tree", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pid := grandchildPID(t, run)
	if !procs.Alive(pid) {
		t.Fatal("the grandchild must be running")
	}
	start := time.Now()
	if _, err := r.Stop("tree", StopOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a forced stop must not wait for a grace period")
	}
	waitDead(t, pid)
	waitDead(t, run.Host.ChildPID)
}

func TestStopUsesThePresetOfTheItem(t *testing.T) {
	r := newRunner(t)
	// The command ignores the first Ctrl+C. The agent preset presses twice,
	// so it ends quickly; the default preset would wait out its grace period.
	addItem(t, r, domain.KindShell, "agentish", "trap", "", func(it *domain.Item) {
		it.Interactive.StopSequence = "agent" // a preset: any item may use it
	})
	run, err := r.Start("agentish", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	output(t, run, "trapping")
	start := time.Now()
	ended, err := r.Stop("agentish", StopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 4*time.Second || ended.Result == nil {
		t.Fatalf("agent preset took %v", time.Since(start))
	}
}

func TestInterruptedRunIsDetectedAndOfferedForRestore(t *testing.T) {
	r := newRunner(t)
	it := addItem(t, r, domain.KindService, "frail", "sleep", "", nil)
	run, err := r.Start("frail", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := os.FindProcess(run.Host.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil { // the host dies without writing a result, like a power loss
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var cur Run
	for time.Now().Before(deadline) {
		cur, _ = inspect(run.Dir)
		if cur.State == StateInterrupted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cur.State != StateInterrupted {
		t.Fatalf("state %q", cur.State)
	}
	if got := r.Interrupted(); len(got) != 1 || got[0].RunID != run.ID {
		t.Fatalf("interrupted %+v", got)
	}
	r.Reconcile(it.ID)
	lines, _ := r.History(it.ID)
	if len(lines) != 1 || lines[0].Outcome != domain.OutcomeInterrupted {
		t.Fatalf("history %+v", lines)
	}
	if len(r.Interrupted()) != 1 {
		t.Fatal("recording history must not forget an interrupted run")
	}
	waitDead(t, run.Host.ChildPID)
}

func TestHeldTerminalCanBeAttachedAndIsClosedByStop(t *testing.T) {
	r := newRunner(t)
	it := addItem(t, r, domain.KindTask, "held", "exit", "2", func(it *domain.Item) { it.Interactive.KeepOpenOnExit = true })
	run, err := r.Start("held", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var cur Run
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cur, _ = inspect(run.Dir); cur.State == StateHeld {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cur.State != StateHeld || cur.Result == nil || cur.Result.ExitCode != 2 {
		t.Fatalf("state %q result %+v", cur.State, cur.Result)
	}
	if _, ok := r.Active(it.ID); ok {
		t.Fatal("a held run is not running: its command has ended")
	}
	if att, ok := r.Attachable(it.ID); !ok || att.ID != run.ID {
		t.Fatal("a held terminal must still be attachable")
	}
	r.Reconcile(it.ID)
	if lines, _ := r.History(it.ID); len(lines) != 1 || lines[0].Outcome != domain.OutcomeFailed {
		t.Fatalf("the result is recorded as soon as the command ends: %+v", lines)
	}
	if len(r.Interrupted()) != 0 {
		t.Fatal("a held run is not an interruption")
	}
	// Stop closes the held terminal.
	ended, err := r.Stop("held", StopOptions{Wait: 15 * time.Second})
	if err != nil || ended.State != StateEnded {
		t.Fatalf("stop: %v %+v", err, ended)
	}
	r.Reconcile(it.ID)
	if lines, _ := r.History(it.ID); len(lines) != 1 {
		t.Fatalf("a run is recorded exactly once, got %d lines", len(lines))
	}
}

func TestEveryKindTakesTheSamePath(t *testing.T) {
	r := newRunner(t)
	for _, kind := range []domain.Kind{domain.KindService, domain.KindTask, domain.KindShell} {
		alias := "kind-" + string(kind)
		addItem(t, r, kind, alias, "sleep", "", nil)
		run, err := r.Start(alias, StartOptions{})
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		spec, _ := host.ReadSpec(run.Dir)
		if spec.Argv[0] != os.Args[0] || spec.Trigger != domain.TriggerManual || spec.KeepOpenOnExit {
			t.Fatalf("%s: spec %+v", kind, spec)
		}
		if _, err := r.Stop(alias, StopOptions{Force: true}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}

func TestLinksAreNotRunnable(t *testing.T) {
	r := newRunner(t)
	err := r.Store.Update(func(f *catalog.File) error {
		it := f.NewItem(domain.KindLink, "docs", "docs", time.Now())
		it.Link = &domain.Link{Target: "https://example.invalid/"}
		f.Items = append(f.Items, it)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start("docs", StartOptions{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestVariantIsAppliedAndRecorded(t *testing.T) {
	r := newRunner(t)
	it := addItem(t, r, domain.KindShell, "varied", "echo", "", func(it *domain.Item) {
		it.Command.Variants = []domain.Variant{{ID: "loud", Env: map[string]string{"EXTRA_FLAG": "yes"}}}
	})
	run, err := r.Start("varied", StartOptions{Variant: "loud"})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := host.ReadSpec(run.Dir)
	found := false
	for _, kv := range spec.Env {
		found = found || kv == "EXTRA_FLAG=yes"
	}
	if spec.Variant != "loud" || !found {
		t.Fatalf("spec %+v", spec)
	}
	if _, err := r.Stop("varied", StopOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if lines, _ := r.History(it.ID); len(lines) != 1 || lines[0].Variant != "loud" {
		t.Fatalf("history %+v", lines)
	}
	if _, err := r.Start("varied", StartOptions{Variant: "nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown variant must be ErrNotFound, got %v", err)
	}
}

func TestCommandTextMatchesWhatRuns(t *testing.T) {
	r := newRunner(t)
	addItem(t, r, domain.KindShell, "copyme", "sleep", "", func(it *domain.Item) {
		it.Command.Variants = []domain.Variant{{ID: "v", AppendArgs: []string{"--flag", "two words"}}}
	})
	bare, err := r.CommandText("copyme", "v", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(bare, " --flag 'two words'") && !strings.HasSuffix(bare, ` --flag "two words"`) {
		t.Fatalf("bare text %q", bare)
	}
	full, err := r.CommandText("copyme", "v", false)
	if err != nil || !strings.Contains(full, bare) || len(full) <= len(bare) {
		t.Fatalf("full text %q (%v)", full, err)
	}
}

func TestStopOfAnIdleItemIsNotFound(t *testing.T) {
	r := newRunner(t)
	addItem(t, r, domain.KindShell, "idle", "sleep", "", nil)
	if _, err := r.Stop("idle", StopOptions{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestRestartStartsANewRun(t *testing.T) {
	r := newRunner(t)
	addItem(t, r, domain.KindService, "again", "sleep", "", nil)
	first, err := r.Start("again", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Restart("again", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.State != StateRunning {
		t.Fatalf("restart gave %+v", second)
	}
	if cur, _ := inspect(first.Dir); cur.State != StateEnded {
		t.Fatalf("the first run must have ended, got %q", cur.State)
	}
}

func TestFindItem(t *testing.T) {
	items := []domain.Item{
		{ID: "i_aaaa111111", Alias: "alpha"},
		{ID: "i_aaaa222222", Alias: "beta"},
		{ID: "i_bbbb333333", Alias: "gamma"},
	}
	cases := []struct {
		ref  string
		want string
		err  error
	}{
		{"alpha", "alpha", nil},
		{"i_bbbb333333", "gamma", nil},
		{"i_bbbb", "gamma", nil},
		{"i_aaaa", "", domain.ErrInvalid},
		{"i_a", "", domain.ErrNotFound}, // too short for a prefix
		{"missing", "", domain.ErrNotFound},
		{"", "", domain.ErrInvalid},
	}
	for _, c := range cases {
		it, err := FindItem(items, c.ref)
		if !errors.Is(err, c.err) || it.Alias != c.want {
			t.Errorf("FindItem(%q) = %q, %v; want %q, %v", c.ref, it.Alias, err, c.want, c.err)
		}
	}
	if _, err := FindItem(items, "i_aaaa"); err == nil || !strings.Contains(err.Error(), "alpha, beta") {
		t.Errorf("an ambiguous reference must name the candidates: %v", err)
	}
}

// fakeRun writes a finished run for pruning tests.
func fakeRun(t *testing.T, r *Runner, itemID string, n int, ended time.Time) string {
	t.Helper()
	spec := domain.RunSpec{
		RunID: fmt.Sprintf("r_%010d", n), ItemID: itemID, Trigger: domain.TriggerManual,
		Argv: []string{"x"}, Cwd: "/", Cols: 80, Rows: 24, CreatedAt: ended.Add(-time.Minute),
	}
	dir := r.runDir(itemID, spec.RunID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := jsonIndent(spec)
	res, _ := jsonIndent(host.Result{ExitCode: 0, EndedAt: ended, Reason: host.ReasonExited})
	for name, data := range map[string][]byte{host.SpecFile: b, host.ResultFile: res, host.LogFile: []byte("log"), host.HostLogFile: []byte("hl")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPruneAppliesRetention(t *testing.T) {
	r := newRunner(t)
	r.Config = func() config.Config {
		c := config.Default()
		c.History = config.History{RunsPerItem: 4, LogsPerItem: 2, MaxAgeDays: 0}
		return c
	}
	now := time.Now()
	r.Now = func() time.Time { return now }
	const item = "i_prune00001"
	var dirs []string
	for n := 1; n <= 6; n++ { // run 6 is the newest
		dirs = append(dirs, fakeRun(t, r, item, n, now.Add(time.Duration(n)*time.Hour)))
	}
	r.Prune(item)
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	for n, dir := range dirs { // index 0 is run 1 (oldest)
		switch {
		case n <= 1: // the two oldest are removed entirely
			if exists(dir) {
				t.Errorf("run %d should be removed", n+1)
			}
		case n <= 3: // kept, but their logs are dropped
			if !exists(dir) || exists(filepath.Join(dir, host.LogFile)) {
				t.Errorf("run %d should keep its record but lose its log", n+1)
			}
		default: // the two newest keep everything
			if !exists(filepath.Join(dir, host.LogFile)) {
				t.Errorf("run %d should keep its log", n+1)
			}
		}
	}
	if lines, _ := r.History(item); len(lines) != 4 {
		t.Fatalf("history must be trimmed to 4 lines, got %d", len(lines))
	}
}

func TestPruneDropsOldRunsAndSparesRunningAndPending(t *testing.T) {
	r := newRunner(t)
	r.Config = func() config.Config {
		c := config.Default()
		c.History = config.History{RunsPerItem: 50, LogsPerItem: 50, MaxAgeDays: 30}
		return c
	}
	now := time.Now()
	r.Now = func() time.Time { return now }
	const item = "i_prune00002"
	oldDir := fakeRun(t, r, item, 1, now.AddDate(0, 0, -40))
	newDir := fakeRun(t, r, item, 2, now.AddDate(0, 0, -1))
	r.Prune(item)
	if _, err := os.Stat(oldDir); err == nil {
		t.Error("a run older than the age limit must be removed")
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Error("a recent run must stay")
	}
	// A run that was interrupted and still waits to be restored is never
	// removed, however old: it has no result, and its host is gone.
	pendingDir := fakeRun(t, r, item, 3, now.AddDate(0, 0, -90))
	if err := os.Remove(filepath.Join(pendingDir, host.ResultFile)); err != nil {
		t.Fatal(err)
	}
	long := now.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(pendingDir, host.SpecFile), long, long); err != nil { // not "still starting"
		t.Fatal(err)
	}
	if err := r.markDesired(Desired{ItemID: item, RunID: "r_0000000003", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	r.Prune(item)
	if _, err := os.Stat(pendingDir); err != nil {
		t.Error("a run pending restore must not be pruned")
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

func TestStatusesSummariseEveryItem(t *testing.T) {
	r := newRunner(t)
	idle := addItem(t, r, domain.KindTask, "never-ran", "sleep", "", nil)
	done := addItem(t, r, domain.KindTask, "done-ok", "exit", "0", nil)
	failed := addItem(t, r, domain.KindTask, "done-bad", "exit", "4", nil)
	live := addItem(t, r, domain.KindService, "live", "sleep", "", nil)
	held := addItem(t, r, domain.KindTask, "held-one", "exit", "0", func(it *domain.Item) { it.Interactive.KeepOpenOnExit = true })
	for _, alias := range []string{"done-ok", "done-bad"} {
		run, err := r.Start(alias, StartOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Wait(run, 15*time.Second); err != nil {
			t.Fatal(err)
		}
		r.Reconcile(run.ItemID)
	}
	liveRun, err := r.Start("live", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	heldRun, err := r.Start("held-one", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the held terminal", 15*time.Second, func() bool {
		cur, _ := inspect(heldRun.Dir)
		return cur.State == StateHeld
	})
	cat, _ := r.Store.Load()
	st := r.Statuses(cat.Items)
	check := func(it domain.Item, state State, last string) {
		t.Helper()
		if got := st[it.ID]; got.State != state || got.Last != last {
			t.Errorf("%s: %+v, want %s / %q", it.Alias, got, state, last)
		}
	}
	check(idle, Idle, "")
	check(done, Idle, "ok")
	check(failed, Idle, "failed (4)")
	check(live, StateRunning, "")
	check(held, StateHeld, "ok")
	if st[live.ID].RunID != liveRun.ID {
		t.Errorf("run id %q", st[live.ID].RunID)
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
