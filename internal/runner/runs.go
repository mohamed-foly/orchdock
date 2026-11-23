package runner

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
)

// State of a run.
type State string

// Run states. They come only from the run's own files and its host: a run is
// running while its host answers, ended once result.json exists, and
// interrupted when the host is gone without having written a result (the
// machine restarted, or the host was killed). A run whose command ended but
// whose terminal is kept open (keepOpenOnExit) is held: it has a result and a
// host, so it can still be attached to and must be closed to finish.
const (
	StateRunning     State = "running"
	StateHeld        State = "held" // the command ended, its terminal is kept open until closed
	StateEnded       State = "ended"
	StateInterrupted State = "interrupted"
)

// Run describes one run of an item.
type Run struct {
	ID     string
	ItemID string
	Dir    string
	State  State
	Spec   domain.RunSpec
	Host   host.Info    // valid while running
	Result *host.Result // set once ended
}

// runsDir is <home>/runs.
func (r *Runner) runsDir() string { return filepath.Join(r.Home, "runs") }

// itemRunsDir is <home>/runs/<itemId>.
func (r *Runner) itemRunsDir(itemID string) string { return filepath.Join(r.runsDir(), itemID) }

// runDir is <home>/runs/<itemId>/<runId>.
func (r *Runner) runDir(itemID, runID string) string {
	return filepath.Join(r.itemRunsDir(itemID), runID)
}

// Inspect reads the current state of the run in dir.
func Inspect(dir string) (Run, bool) { return inspect(dir) }

// inspect reads the state of the run in dir.
func inspect(dir string) (Run, bool) {
	spec, err := host.ReadSpec(dir)
	if err != nil {
		return Run{}, false
	}
	run := Run{ID: spec.RunID, ItemID: spec.ItemID, Dir: dir, Spec: spec}
	if res, err := host.ReadResult(dir); err == nil {
		run.State, run.Result = StateEnded, &res
		if info, err := host.ReadInfo(dir); err == nil && host.Ping(info) == nil {
			run.State, run.Host = StateHeld, info
		}
		return run, true
	}
	if info, err := host.ReadInfo(dir); err == nil && host.Ping(info) == nil {
		run.State, run.Host = StateRunning, info
		return run, true
	}
	// A host that is still starting has spec.json but neither file yet: treat a
	// very young run as running so it is not reported as lost.
	if st, err := os.Stat(filepath.Join(dir, host.SpecFile)); err == nil && time.Since(st.ModTime()) < startGrace {
		if _, err := os.Stat(filepath.Join(dir, host.InfoFile)); err != nil {
			run.State = StateRunning
			return run, true
		}
	}
	run.State = StateInterrupted
	return run, true
}

// startGrace is how long a freshly written spec may exist without a host.json.
const startGrace = 10 * time.Second

// Runs lists the runs of one item, newest first.
func (r *Runner) Runs(itemID string) ([]Run, error) {
	entries, err := os.ReadDir(r.itemRunsDir(itemID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Run
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if run, ok := inspect(filepath.Join(r.itemRunsDir(itemID), e.Name())); ok {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec.CreatedAt.After(out[j].Spec.CreatedAt) })
	return out, nil
}

// AllRuns lists the runs of every item.
func (r *Runner) AllRuns() ([]Run, error) {
	entries, err := os.ReadDir(r.runsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Run
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runs, err := r.Runs(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, runs...)
	}
	return out, nil
}

// Active returns the item's running run, if any.
func (r *Runner) Active(itemID string) (Run, bool) {
	runs, _ := r.Runs(itemID)
	for _, run := range runs {
		if run.State == StateRunning {
			return run, true
		}
	}
	return Run{}, false
}

// Attachable returns the item's run that has a live host: the running run, or
// a held one whose terminal is still open.
func (r *Runner) Attachable(itemID string) (Run, bool) {
	runs, _ := r.Runs(itemID)
	for _, run := range runs {
		if run.State == StateRunning || run.State == StateHeld {
			return run, true
		}
	}
	return Run{}, false
}

// activeCount counts running runs across all items.
func (r *Runner) activeCount() int {
	runs, _ := r.AllRuns()
	n := 0
	for _, run := range runs {
		if run.State == StateRunning {
			n++
		}
	}
	return n
}
