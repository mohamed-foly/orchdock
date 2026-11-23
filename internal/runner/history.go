package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// recordedMarker is created in a run directory once its history line is written.
const recordedMarker = ".recorded"

var historyMu sync.Mutex

func (r *Runner) historyPath(itemID string) string {
	return filepath.Join(r.Home, "history", itemID+".jsonl")
}

func jsonIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// appendHistory appends one line to history/<itemId>.jsonl.
func (r *Runner) appendHistory(itemID string, line domain.HistoryLine) {
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	historyMu.Lock()
	defer historyMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(r.historyPath(itemID)), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(r.historyPath(itemID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Record appends a history line that has no run, such as a skipped scheduled run.
func (r *Runner) Record(itemID string, line domain.HistoryLine) error {
	if err := line.Validate(); err != nil {
		return err
	}
	r.appendHistory(itemID, line)
	return nil
}

// History returns the item's history lines, oldest first.
func (r *Runner) History(itemID string) ([]domain.HistoryLine, error) {
	b, err := os.ReadFile(r.historyPath(itemID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []domain.HistoryLine
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var l domain.HistoryLine
		if json.Unmarshal(sc.Bytes(), &l) == nil {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

// Reconcile writes the history line of every ended or interrupted run of the
// item that has none yet, and forgets ended runs in desired.json. It is safe to
// call at any time and as often as wanted: a run is recorded exactly once, even
// if it ended while nothing was watching.
func (r *Runner) Reconcile(itemID string) {
	runs, _ := r.Runs(itemID)
	for _, run := range runs {
		if run.State == StateRunning {
			continue
		}
		ended := run.State == StateEnded || run.State == StateHeld
		marker := filepath.Join(run.Dir, recordedMarker)
		if _, err := os.Stat(marker); err == nil {
			continue
		}
		line := historyLine(run, r.now())
		if _, err := os.Stat(filepath.Join(run.Dir, timeoutMarker)); err == nil && line.Outcome != domain.OutcomeInterrupted {
			line.Outcome = domain.OutcomeTimeout
		}
		r.appendHistory(itemID, line)
		_ = os.WriteFile(marker, nil, 0o600)
		if ended {
			r.clearDesiredRun(itemID, run.ID)
		}
	}
}

// clearDesiredRun forgets the entry only if it belongs to the given run, so a
// newer run of the same item is not forgotten.
func (r *Runner) clearDesiredRun(itemID, runID string) {
	desiredMu.Lock()
	defer desiredMu.Unlock()
	list := r.readDesired()
	kept := list[:0:0]
	for _, d := range list {
		if d.ItemID == itemID && d.RunID == runID {
			continue
		}
		kept = append(kept, d)
	}
	if len(kept) != len(list) {
		_ = r.writeDesired(kept)
	}
}

// ReconcileAll reconciles every item that has runs.
func (r *Runner) ReconcileAll() {
	entries, err := os.ReadDir(r.runsDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			r.Reconcile(e.Name())
		}
	}
}

// historyLine turns a finished run into its history line.
func historyLine(run Run, now time.Time) domain.HistoryLine {
	l := domain.HistoryLine{
		RunID:        run.ID,
		Trigger:      run.Spec.Trigger,
		Variant:      run.Spec.Variant,
		ScheduledFor: run.Spec.ScheduledFor,
		StartedAt:    run.Spec.CreatedAt,
		Log:          filepath.ToSlash(filepath.Join("runs", run.ItemID, run.ID, "output.log")),
	}
	switch {
	case (run.State == StateEnded || run.State == StateHeld) && run.Result != nil:
		code := run.Result.ExitCode
		l.ExitCode, l.EndedAt = &code, run.Result.EndedAt
		switch {
		case run.Result.Reason == "killed":
			l.Outcome = domain.OutcomeKilled
		case code == 0:
			l.Outcome = domain.OutcomeOK
		default:
			l.Outcome = domain.OutcomeFailed
		}
	default:
		l.Outcome, l.EndedAt = domain.OutcomeInterrupted, now.UTC()
	}
	if !l.EndedAt.IsZero() && !l.StartedAt.IsZero() {
		l.DurationMs = max(0, l.EndedAt.Sub(l.StartedAt).Milliseconds())
	}
	return l
}
