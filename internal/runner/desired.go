package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mohamed-foly/orchdock/internal/fsutil"
)

// Desired is an entry of state/desired.json: a run that should be running, so
// that after a restart of the machine it can be offered for restore.
type Desired struct {
	ItemID    string    `json:"itemId"`
	RunID     string    `json:"runId"`
	Variant   string    `json:"variant,omitempty"`
	StartedAt time.Time `json:"startedAt"`
}

var desiredMu sync.Mutex

func (r *Runner) desiredPath() string { return filepath.Join(r.Home, "state", "desired.json") }

func (r *Runner) readDesired() []Desired {
	b, err := os.ReadFile(r.desiredPath())
	if err != nil {
		return nil
	}
	var out []Desired
	_ = json.Unmarshal(b, &out)
	return out
}

func (r *Runner) writeDesired(list []Desired) error {
	if err := os.MkdirAll(filepath.Dir(r.desiredPath()), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(r.desiredPath(), b, 0o600)
}

// markDesired records that a run should be running.
func (r *Runner) markDesired(d Desired) error {
	desiredMu.Lock()
	defer desiredMu.Unlock()
	list := slices.DeleteFunc(r.readDesired(), func(x Desired) bool { return x.ItemID == d.ItemID })
	return r.writeDesired(append(list, d))
}

// clearDesired removes an item's entry (the user stopped it, or it ended).
func (r *Runner) clearDesired(itemID string) {
	desiredMu.Lock()
	defer desiredMu.Unlock()
	list := r.readDesired()
	kept := slices.DeleteFunc(slices.Clone(list), func(x Desired) bool { return x.ItemID == itemID })
	if len(kept) != len(list) {
		_ = r.writeDesired(kept)
	}
}

// Interrupted returns the runs that were running and whose host is gone
// without a result: the work lost to a restart of the machine or a crash.
func (r *Runner) Interrupted() []Desired {
	var out []Desired
	for _, d := range r.readDesired() {
		run, ok := inspect(r.runDir(d.ItemID, d.RunID))
		if ok && run.State == StateInterrupted {
			out = append(out, d)
		}
	}
	return out
}
