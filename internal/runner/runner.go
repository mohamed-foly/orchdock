// Package runner starts, stops and records runs. It is the one place that
// knows how to run an item, so the CLI, the service, the scheduler and the
// tray behave identically.
//
// Every kind of item (agent, service, task, shell) is just a command in a
// terminal and takes the same path: resolve the command, write a run spec, and
// let a detached host process own the terminal. The kind only chooses defaults
// such as the stop preset. The single exception is a link, which is a shortcut
// with nothing to run.
package runner

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/command"
	"github.com/mohamed-foly/orchdock/internal/config"
	"github.com/mohamed-foly/orchdock/internal/detach"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/fsutil"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/ports"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

// Runner runs items.
type Runner struct {
	Home  string
	Store *catalog.Store
	// Exe is the OrchDock binary that runs hosts ("<Exe> <HostArgs> <runDir>").
	Exe      string
	HostArgs []string                     // default: host
	HostKind detach.Kind                  // Console for orchdock, GUI for orchdockw
	HostEnv  func(runDir string) []string // nil: inherit this process's environment
	// Input builds the command of an item. The default uses the item's own
	// command; a caller with another source of commands (agent profiles)
	// replaces it.
	Input  func(item domain.Item, project *domain.Project) (command.Input, error)
	Config func() config.Config // nil: defaults
	CmdEnv command.Env          // the machine commands are resolved for; zero: this machine
	Now    func() time.Time     // nil: time.Now
}

// New returns a Runner for a home whose hosts are started with exe.
func New(home string, store *catalog.Store, exe string) *Runner {
	return &Runner{Home: home, Store: store, Exe: exe}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) config() config.Config {
	if r.Config != nil {
		return r.Config()
	}
	return config.Default()
}

func (r *Runner) input(item domain.Item, project *domain.Project) (command.Input, error) {
	if r.Input != nil {
		return r.Input(item, project)
	}
	return command.FromItem(item, project, nil), nil
}

// StartOptions tune one start.
type StartOptions struct {
	Variant string            // a variant id; empty uses the defaults
	Trigger string            // why the run starts; empty means manual
	Vars    map[string]string // extra placeholder values
	Cols    int               // terminal size; zero uses 120x32
	Rows    int
	// ScheduledFor is the schedule slot a scheduled or catch-up run is for.
	ScheduledFor time.Time
}

// Default terminal size for a new run.
const (
	defaultCols = 120
	defaultRows = 32
)

// resolve finds the item and resolves its command.
func (r *Runner) resolve(ref, variant string, vars map[string]string) (domain.Item, command.Resolved, error) {
	cat, err := r.Store.Load()
	if err != nil {
		return domain.Item{}, command.Resolved{}, err
	}
	item, err := FindItem(cat.Items, ref)
	if err != nil {
		return item, command.Resolved{}, err
	}
	if item.Kind == domain.KindLink {
		return item, command.Resolved{}, fmt.Errorf("%w: %s is a shortcut, there is nothing to run", domain.ErrInvalid, item.Alias)
	}
	// the item's main tag (its first tag with a folder) gives the default
	// working folder and {projectDir} / {projectName}
	project := domain.ProjectOf(cat.HomeOf(item))
	in, err := r.input(item, project)
	if err != nil {
		return item, command.Resolved{}, err
	}
	merged := map[string]string{}
	maps.Copy(merged, in.Vars)
	if item.Service != nil && item.Service.Port > 0 {
		merged["port"] = fmt.Sprint(item.Service.Port)
	}
	maps.Copy(merged, vars)
	in.Vars = merged
	res, err := command.Resolve(in, variant, r.CmdEnv)
	return item, res, err
}

// CommandText returns the command of an item as text to paste into a terminal
// (what the Copy button and "orchdock cmd" show).
func (r *Runner) CommandText(ref, variant string, bare bool) (string, error) {
	_, res, err := r.resolve(ref, variant, nil)
	if err != nil {
		return "", err
	}
	return res.CopyText(bare), nil
}

// Start runs an item. It fails with ErrConflict when the item already has an
// active run or too many runs are active, and with ErrStartFailed (carrying
// the end of the host's log) when the host does not come up.
func (r *Runner) Start(ref string, o StartOptions) (Run, error) {
	if o.Trigger == "" {
		o.Trigger = domain.TriggerManual
	}
	if err := r.ensurePort(ref); err != nil {
		return Run{}, err
	}
	item, res, err := r.resolve(ref, o.Variant, o.Vars)
	if err != nil {
		if item.ID != "" {
			r.recordStartFailure(item, o, "")
		}
		return Run{}, err
	}
	if active, ok := r.Active(item.ID); ok {
		return Run{}, fmt.Errorf("%w: %s is already running (run %s)", domain.ErrConflict, item.Alias, active.ID)
	}
	cfg := r.config()
	if cfg.MaxConcurrentRuns > 0 && r.activeCount() >= cfg.MaxConcurrentRuns {
		return Run{}, fmt.Errorf("%w: %d runs are already active (limit %d)", domain.ErrConflict, r.activeCount(), cfg.MaxConcurrentRuns)
	}
	if st, err := os.Stat(res.Cwd); err != nil || !st.IsDir() {
		r.recordStartFailure(item, o, res.Variant)
		return Run{}, fmt.Errorf("%w: the working folder %q does not exist", domain.ErrInvalid, res.Cwd)
	}
	if s := item.Service; s != nil && s.Port > 0 && ports.IsListening(s.Port) {
		// Never move a service to another port silently: its address must stay the same.
		who := ports.Holder(s.Port)
		if who == "" {
			who = "another program"
		}
		return Run{}, fmt.Errorf("%w: port %d is already in use by %s; stop it or change the port of %s", domain.ErrConflict, s.Port, who, item.Alias)
	}
	r.Prune(item.ID)

	cols, rows := o.Cols, o.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = defaultCols, defaultRows
	}
	spec := domain.RunSpec{
		RunID:           domain.NewID(domain.PrefixRun),
		ItemID:          item.ID,
		Trigger:         o.Trigger,
		ScheduledFor:    o.ScheduledFor,
		Variant:         res.Variant,
		Argv:            res.Argv,
		Cwd:             res.Cwd,
		Env:             res.Env,
		Cols:            cols,
		Rows:            rows,
		KeepOpenOnExit:  item.Interactive.KeepOpenOnExit,
		ScrollbackBytes: cfg.Terminal.HostScrollbackBytes,
		LogMaxBytes:     cfg.Terminal.LogMaxBytes,
		CreatedAt:       r.now().UTC(),
	}
	if err := spec.Validate(); err != nil {
		return Run{}, err
	}
	dir := r.runDir(item.ID, spec.RunID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Run{}, err
	}
	b, err := jsonIndent(spec)
	if err != nil {
		return Run{}, err
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, host.SpecFile), b, 0o600); err != nil {
		return Run{}, err
	}

	args := r.HostArgs
	if args == nil {
		args = []string{"host"}
	}
	opts := host.LaunchOptions{Exe: r.Exe, Args: args, RunDir: dir, Kind: r.HostKind}
	if r.HostEnv != nil {
		opts.Env = r.HostEnv(dir)
	}
	info, err := host.Launch(opts)
	if err != nil {
		r.appendHistory(item.ID, domain.HistoryLine{
			RunID: spec.RunID, Trigger: o.Trigger, Variant: res.Variant,
			StartedAt: spec.CreatedAt, EndedAt: r.now().UTC(), Outcome: domain.OutcomeStartFailed,
		})
		_ = os.WriteFile(filepath.Join(dir, recordedMarker), nil, 0o600)
		return Run{}, fmt.Errorf("%w: %v", domain.ErrStartFailed, err)
	}

	_ = r.Store.Update(func(f *catalog.File) error {
		for i := range f.Items {
			if f.Items[i].ID == item.ID {
				f.Items[i].LastRunAt = spec.CreatedAt
			}
		}
		return nil
	})
	if o.Trigger != domain.TriggerSchedule && o.Trigger != domain.TriggerCatchup {
		_ = r.markDesired(Desired{ItemID: item.ID, RunID: spec.RunID, Variant: res.Variant, StartedAt: spec.CreatedAt})
	}
	r.Reconcile(item.ID)
	return Run{ID: spec.RunID, ItemID: item.ID, Dir: dir, State: StateRunning, Spec: spec, Host: info}, nil
}

// ensurePort gives a service in auto port mode its port the first time it
// starts: the lowest free port of its range (or the configured one) that no
// other item uses. The port is saved, so it stays the same from then on.
func (r *Runner) ensurePort(ref string) error {
	cat, err := r.Store.Load()
	if err != nil {
		return err
	}
	item, err := FindItem(cat.Items, ref)
	if err != nil || item.Service == nil || item.Service.Port > 0 || item.Service.PortMode != "auto" {
		return nil // errors surface in resolve
	}
	return r.Store.Update(func(f *catalog.File) error {
		taken := map[int]bool{}
		var me *domain.Item
		for i := range f.Items {
			it := &f.Items[i]
			if it.ID == item.ID {
				me = it
			} else if it.Service != nil && it.Service.Port > 0 {
				taken[it.Service.Port] = true
			}
		}
		if me == nil || me.Service == nil || me.Service.Port > 0 {
			return nil // changed meanwhile
		}
		lo, hi := r.config().ServicePortRange[0], r.config().ServicePortRange[1]
		if pr := me.Service.PortRange; pr != nil {
			lo, hi = pr[0], pr[1]
		}
		p, err := ports.Allocate(lo, hi, taken, nil)
		if err != nil {
			return fmt.Errorf("%w: %s needs a port: %v", domain.ErrConflict, me.Alias, err)
		}
		me.Service.Port = p
		return nil
	})
}

// recordStartFailure writes a start-failed history line for a run that never got a host.
func (r *Runner) recordStartFailure(item domain.Item, o StartOptions, variant string) {
	r.appendHistory(item.ID, domain.HistoryLine{
		RunID: domain.NewID(domain.PrefixRun), Trigger: o.Trigger, Variant: variant,
		StartedAt: r.now().UTC(), EndedAt: r.now().UTC(), Outcome: domain.OutcomeStartFailed,
	})
}

// StopOptions tune one stop.
type StopOptions struct {
	Force bool          // end the whole tree at once
	Wait  time.Duration // how long to wait for the run to end; zero means 20 s
	// Timeout records the run as "timeout" instead of "killed" (the scheduler
	// stops runs that exceed their task's time limit).
	Timeout bool
}

// timeoutMarker in a run directory makes its history line say "timeout".
const timeoutMarker = ".timeout"

// Stop ends the item's active run: the item's stop preset (interrupt, wait,
// terminate), or immediately with Force. It waits until the run has ended.
func (r *Runner) Stop(ref string, o StopOptions) (Run, error) {
	cat, err := r.Store.Load()
	if err != nil {
		return Run{}, err
	}
	item, err := FindItem(cat.Items, ref)
	if err != nil {
		return Run{}, err
	}
	run, ok := r.Attachable(item.ID)
	if !ok {
		return Run{}, fmt.Errorf("%w: %s is not running", domain.ErrNotFound, item.Alias)
	}
	sig := wire.SignalMsg{Signal: wire.SigStop} // for a held run any signal closes its terminal
	switch {
	case o.Force:
		sig.Signal = wire.SigKill
	case item.Interactive.StopSequence == "agent":
		sig.Signal = wire.SigStopAgent
	case item.Interactive.StopSequence == "custom":
		sig.Text = item.Interactive.StopText
	}
	r.clearDesired(item.ID) // the user chose this: not an interruption
	if o.Timeout {
		_ = os.WriteFile(filepath.Join(run.Dir, timeoutMarker), nil, 0o600)
	}
	if err := host.SendSignal(run.Dir, sig); err != nil {
		return Run{}, err
	}
	wait := o.Wait
	if wait == 0 {
		wait = 20 * time.Second
	}
	ended, err := r.Wait(run, wait)
	if err != nil {
		return ended, err
	}
	r.Reconcile(item.ID)
	return ended, nil
}

// Restart stops the item if it runs and starts it again.
func (r *Runner) Restart(ref string, o StartOptions) (Run, error) {
	if _, err := r.Stop(ref, StopOptions{}); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Run{}, err
	}
	return r.Start(ref, o)
}

// Wait blocks until the run has ended or was lost, or the timeout (zero: no
// limit) passes. The returned Run has its result when it ended.
func (r *Runner) Wait(run Run, timeout time.Duration) (Run, error) {
	deadline := time.Now().Add(timeout)
	for {
		cur, ok := inspect(run.Dir)
		if ok && cur.State != StateRunning {
			return cur, nil
		}
		if timeout > 0 && time.Now().After(deadline) {
			return cur, fmt.Errorf("run %s did not end within %s", run.ID, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
