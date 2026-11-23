package runner

import (
	"fmt"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// Idle is the state of an item with no live run.
const Idle State = "idle"

// Status is what a UI shows about an item: its state now and how it last ended.
type Status struct {
	State     State     `json:"state"`           // running, held, interrupted or idle
	RunID     string    `json:"runId,omitempty"` // the live (or interrupted) run
	Last      string    `json:"last,omitempty"`  // outcome of the last finished run: "ok", "failed (3)", ...
	LastRunAt time.Time `json:"lastRunAt,omitzero"`
}

// Statuses returns the status of every item. A running run wins over a held
// one, which wins over the newest run being interrupted; otherwise the item is idle.
func (r *Runner) Statuses(items []domain.Item) map[string]Status {
	out := make(map[string]Status, len(items))
	for _, it := range items {
		st := Status{State: Idle, LastRunAt: it.LastRunAt}
		r.Reconcile(it.ID)       // a run that just ended has its history line before we read it
		runs, _ := r.Runs(it.ID) // newest first
		for i, run := range runs {
			switch {
			case run.State == StateRunning:
				st.State, st.RunID = StateRunning, run.ID
			case run.State == StateHeld && st.State != StateRunning:
				st.State, st.RunID = StateHeld, run.ID
			case i == 0 && run.State == StateInterrupted && st.State == Idle:
				st.State, st.RunID = StateInterrupted, run.ID
			}
		}
		if hist, _ := r.History(it.ID); len(hist) > 0 {
			st.Last = lastText(hist[len(hist)-1])
		}
		out[it.ID] = st
	}
	return out
}

// lastText describes a history line in a few words.
func lastText(l domain.HistoryLine) string {
	if l.Outcome == domain.OutcomeFailed && l.ExitCode != nil {
		return fmt.Sprintf("failed (%d)", *l.ExitCode)
	}
	return l.Outcome
}
