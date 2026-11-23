package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/fsutil"
)

// Rules.
const (
	onTimeWindow = 2 * time.Minute  // a slot this recent counts as on time
	heartbeat    = 30 * time.Second // longest wait between evaluations
	missedLimit  = 1000             // stop counting missed slots here
)

// StateFile is the scheduler state, inside the home folder.
const StateFile = "state/scheduler.json"

// ItemState is what the scheduler remembers about one task.
type ItemState struct {
	Spec      string    `json:"spec"`             // the schedule this state belongs to
	Enabled   bool      `json:"enabled"`          // as last seen
	LastSlot  time.Time `json:"lastSlot"`         // the newest slot already handled
	NextDue   time.Time `json:"nextDue,omitzero"` // the next slot
	LastRunAt time.Time `json:"lastRunAt,omitzero"`
	Queued    time.Time `json:"queued,omitzero"`   // a slot waiting for the running run to end
	Deadline  time.Time `json:"deadline,omitzero"` // time limit of the run it started
}

// Hooks connect the scheduler to the rest of OrchDock.
type Hooks struct {
	Items  func() []domain.Item                                       // the catalog's items
	Active func(itemID string) bool                                   // the item has a live run
	Start  func(itemID, trigger string, scheduledFor time.Time) error // start a run
	Stop   func(itemID string, timeout bool)                          // stop the live run
	Record func(itemID string, line domain.HistoryLine)               // a history line without a run
}

// Scheduler decides when tasks run. Create it with New.
type Scheduler struct {
	Hooks
	home string
	now  func() time.Time
	kick chan struct{}

	mu    sync.Mutex
	state map[string]*ItemState
}

// New returns a scheduler for the home folder. now is the clock (time.Now in
// the service, a fake clock in tests).
func New(home string, now func() time.Time, h Hooks) *Scheduler {
	s := &Scheduler{Hooks: h, home: home, now: now, kick: make(chan struct{}, 1), state: map[string]*ItemState{}}
	s.load()
	return s
}

func (s *Scheduler) path() string { return filepath.Join(s.home, filepath.FromSlash(StateFile)) }

func (s *Scheduler) load() {
	b, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.state)
	if s.state == nil {
		s.state = map[string]*ItemState{}
	}
}

func (s *Scheduler) save() {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path()), 0o700); err == nil {
		_ = fsutil.WriteFileAtomic(s.path(), b, 0o600)
	}
}

// Next returns when an item's schedule fires next (zero: not scheduled).
func (s *Scheduler) Next(itemID string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.state[itemID]; st != nil && st.Enabled {
		return st.NextDue
	}
	return time.Time{}
}

// Kick asks for an evaluation now (the catalog changed).
func (s *Scheduler) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run evaluates until ctx ends: at every due slot, after every kick and at
// least every 30 s. Evaluation reads the wall clock each time, so a computer
// that slept through slots is handled exactly like one that was switched off.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		s.Evaluate()
		wait := heartbeat
		if next := s.nextDue(); !next.IsZero() {
			if d := next.Sub(s.now()); d < wait {
				wait = max(d, time.Second)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-time.After(wait):
		}
	}
}

func (s *Scheduler) nextDue() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next time.Time
	for _, st := range s.state {
		if st.Enabled && !st.NextDue.IsZero() && (next.IsZero() || st.NextDue.Before(next)) {
			next = st.NextDue
		}
	}
	return next
}

// Evaluate applies the rules once at the current time.
func (s *Scheduler) Evaluate() {
	now := s.now().Round(0) // the wall clock: monotonic time can stand still during sleep
	items := s.Items()
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, it := range items {
		if it.Task == nil || it.Task.Schedule == nil {
			continue
		}
		seen[it.ID] = true
		s.evaluateItem(it, now)
	}
	for id := range s.state {
		if !seen[id] {
			delete(s.state, id) // the item or its schedule is gone
		}
	}
	s.save()
}

func (s *Scheduler) evaluateItem(it domain.Item, now time.Time) {
	sched, task := *it.Task.Schedule, it.Task
	st := s.state[it.ID]
	spec := sched.Spec()
	switch {
	case st == nil || st.Spec != spec:
		// a new or changed schedule never catches up on the time before it existed
		st = &ItemState{Spec: spec, Enabled: sched.Enabled, LastSlot: now}
		s.state[it.ID] = st
	case !st.Enabled && sched.Enabled:
		st.LastSlot = now // re-enabled: no burst of old slots
	}
	st.Enabled = sched.Enabled
	if !sched.Enabled {
		st.NextDue, st.Queued = time.Time{}, time.Time{}
		return
	}
	active := s.Active(it.ID)
	if !st.Deadline.IsZero() {
		switch {
		case !active:
			st.Deadline = time.Time{}
		case now.After(st.Deadline):
			st.Deadline = time.Time{}
			s.Stop(it.ID, true)
			active = false
		}
	}
	if !st.Queued.IsZero() && !active {
		slot := st.Queued
		st.Queued = time.Time{}
		s.start(it, st, domain.TriggerSchedule, slot, now)
		active = true
	}
	if n, latest := Missed(sched, st.LastSlot, now, missedLimit); n > 0 {
		age := now.Sub(latest)
		switch {
		case age <= onTimeWindow:
			s.fire(it, st, domain.TriggerSchedule, latest, now, active)
		case task.Missed == "catchup" && age <= time.Duration(max(task.CatchUpMaxAgeHours, 1))*time.Hour:
			s.fire(it, st, domain.TriggerCatchup, latest, now, active) // one run, however many were missed
		default:
			s.Record(it.ID, domain.HistoryLine{Trigger: domain.TriggerSchedule, ScheduledFor: latest, Outcome: domain.OutcomeSkippedMissed, Count: n})
		}
		st.LastSlot = latest
	}
	st.NextDue = NextAfter(sched, now)
}

// fire runs a due slot, minding a run that is still going.
func (s *Scheduler) fire(it domain.Item, st *ItemState, trigger string, slot, now time.Time, active bool) {
	if active {
		switch it.Task.Overlap {
		case "queue":
			st.Queued = slot // only one waits; a newer slot replaces it
			return
		case "restart":
			s.Stop(it.ID, false)
		default: // skip
			s.Record(it.ID, domain.HistoryLine{Trigger: trigger, ScheduledFor: slot, Outcome: domain.OutcomeSkippedOverlap})
			return
		}
	}
	s.start(it, st, trigger, slot, now)
}

func (s *Scheduler) start(it domain.Item, st *ItemState, trigger string, slot, now time.Time) {
	if err := s.Start(it.ID, trigger, slot); err != nil {
		return // the runner records why it could not start
	}
	st.LastRunAt = now
	if m := it.Task.TimeoutMinutes; m > 0 {
		st.Deadline = now.Add(time.Duration(m) * time.Minute)
	}
}
