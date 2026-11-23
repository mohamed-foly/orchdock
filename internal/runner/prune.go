package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/mohamed-foly/orchdock/internal/fsutil"
	"github.com/mohamed-foly/orchdock/internal/host"
)

// Prune applies the history retention settings to one item: it keeps the
// newest RunsPerItem runs, keeps logs for the newest LogsPerItem of them, drops
// runs older than MaxAgeDays, and trims the history file to RunsPerItem lines.
// A zero setting means no limit. Running runs and runs still waiting to be
// restored are never touched.
func (r *Runner) Prune(itemID string) {
	r.Reconcile(itemID) // every run has its history line before anything is removed
	h := r.config().History
	pending := map[string]bool{}
	for _, d := range r.readDesired() {
		pending[d.RunID] = true
	}
	var cutoff time.Time
	if h.MaxAgeDays > 0 {
		cutoff = r.now().AddDate(0, 0, -h.MaxAgeDays)
	}
	runs, _ := r.Runs(itemID) // newest first
	finished := 0
	for _, run := range runs {
		if run.State == StateRunning || run.State == StateHeld || pending[run.ID] {
			continue
		}
		n := finished
		finished++
		old := !cutoff.IsZero() && run.Result != nil && run.Result.EndedAt.Before(cutoff)
		switch {
		case (h.RunsPerItem > 0 && n >= h.RunsPerItem) || old:
			_ = os.RemoveAll(run.Dir)
		case h.LogsPerItem > 0 && n >= h.LogsPerItem:
			_ = os.Remove(filepath.Join(run.Dir, host.LogFile))
			_ = os.Remove(filepath.Join(run.Dir, host.HostLogFile))
		}
	}
	if h.RunsPerItem > 0 {
		r.trimHistory(itemID, h.RunsPerItem)
	}
}

// trimHistory keeps the last keep lines of the item's history file.
func (r *Runner) trimHistory(itemID string, keep int) {
	historyMu.Lock()
	defer historyMu.Unlock()
	b, err := os.ReadFile(r.historyPath(itemID))
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) <= keep {
		return
	}
	out := append(bytes.Join(lines[len(lines)-keep:], []byte("\n")), '\n')
	_ = fsutil.WriteFileAtomic(r.historyPath(itemID), out, 0o600)
}
