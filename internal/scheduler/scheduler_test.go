package scheduler

import (
	"fmt"
	"testing"
	"time"
	_ "time/tzdata" // DST tests need zone data on every OS

	"github.com/mohamed-foly/orchdock/internal/domain"
)

var berlin = mustLoc("Europe/Berlin")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func at2(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, berlin) }

func sched(t *testing.T, spec string) *domain.Schedule {
	t.Helper()
	s, err := domain.ParseSpec(spec, at2(2026, 1, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return &s
}

// world is a fake OrchDock: a clock, items, runs and history.
type world struct {
	t       *testing.T
	clock   time.Time
	items   []domain.Item
	active  map[string]bool
	started []string // "trigger@15:04"
	stopped []string // "id" or "id:timeout"
	history []domain.HistoryLine
	s       *Scheduler
}

func newWorld(t *testing.T, start time.Time, items ...domain.Item) *world {
	w := &world{t: t, clock: start, items: items, active: map[string]bool{}}
	w.s = New(t.TempDir(), func() time.Time { return w.clock }, Hooks{
		Items:  func() []domain.Item { return w.items },
		Active: func(id string) bool { return w.active[id] },
		Start: func(id, trigger string, slot time.Time) error {
			w.started = append(w.started, fmt.Sprintf("%s@%s", trigger, slot.Format("01-02 15:04")))
			w.active[id] = true
			return nil
		},
		Stop: func(id string, timeout bool) {
			w.active[id] = false
			if timeout {
				id += ":timeout"
			}
			w.stopped = append(w.stopped, id)
		},
		Record: func(_ string, l domain.HistoryLine) { w.history = append(w.history, l) },
	})
	w.s.Evaluate() // OrchDock was running at the start time
	return w
}

// at moves the clock (no ticks in between, like sleep or power-off) and evaluates.
func (w *world) at(t time.Time) { w.clock = t; w.s.Evaluate() }

func task(id, spec, missed string, t *testing.T) domain.Item {
	it := domain.Item{ID: id, Kind: domain.KindTask, Task: &domain.Task{Schedule: sched(t, spec), Missed: missed, CatchUpMaxAgeHours: 24, Overlap: "skip"}}
	return it
}

func TestNextAfter(t *testing.T) {
	cases := []struct {
		spec string
		from time.Time
		want time.Time
	}{
		{"daily 09:00,18:30", at2(2026, 3, 10, 8, 0), at2(2026, 3, 10, 9, 0)},
		{"daily 09:00,18:30", at2(2026, 3, 10, 9, 0), at2(2026, 3, 10, 18, 30)},
		{"daily 09:00,18:30", at2(2026, 3, 10, 19, 0), at2(2026, 3, 11, 9, 0)},
		{"weekly mon,thu 09:00", at2(2026, 3, 10, 10, 0), at2(2026, 3, 12, 9, 0)}, // Tue -> Thu
		{"weekly mon 09:00", at2(2026, 3, 9, 9, 0), at2(2026, 3, 16, 9, 0)},
		{"monthly 31 08:00", at2(2026, 2, 1, 0, 0), at2(2026, 2, 28, 8, 0)}, // last day of February
		{"monthly 1 08:00", at2(2026, 12, 2, 0, 0), at2(2027, 1, 1, 8, 0)},
		{"every 2h", at2(2026, 1, 1, 3, 0), at2(2026, 1, 1, 4, 0)}, // anchored at 00:00
		{`cron "*/15 * * * *"`, at2(2026, 3, 10, 8, 7), at2(2026, 3, 10, 8, 15)},
	}
	for _, c := range cases {
		if got := NextAfter(*sched(t, c.spec), c.from); !got.Equal(c.want) {
			t.Errorf("%s after %s = %s, want %s", c.spec, c.from, got, c.want)
		}
	}
}

func TestDaylightSaving(t *testing.T) {
	s := *sched(t, "daily 02:30")
	// spring forward (29 March 2026, 02:00 -> 03:00): 02:30 does not exist, it runs at 03:30 once
	spring := NextAfter(s, at2(2026, 3, 29, 0, 0))
	if h, m, _ := spring.Clock(); spring.Day() != 29 || h != 3 || m != 30 {
		t.Fatalf("spring forward: %s", spring)
	}
	if next := NextAfter(s, spring); next.Day() != 30 {
		t.Fatalf("after the spring slot: %s", next)
	}
	// fall back (25 October 2026, 03:00 -> 02:00): 02:30 happens twice, it runs once
	fall := NextAfter(s, at2(2026, 10, 25, 0, 0))
	if fall.Day() != 25 {
		t.Fatalf("fall back: %s", fall)
	}
	if next := NextAfter(s, fall); next.Day() != 26 {
		t.Fatalf("the repeated hour must not run twice: %s then %s", fall, next)
	}
	if n, _ := Missed(s, at2(2026, 10, 24, 12, 0), at2(2026, 10, 26, 12, 0), 100); n != 2 {
		t.Fatalf("slots over the fall-back weekend: %d, want 2", n)
	}
}

func TestOffThroughASlot(t *testing.T) {
	skip := task("skip", "daily 09:00", "skip", t)
	catchup := task("catchup", "daily 09:00", "catchup", t)
	w := newWorld(t, at2(2026, 3, 10, 8, 0), skip, catchup) // first evaluation at 08:00, then off
	w.at(at2(2026, 3, 10, 12, 0))                           // back at 12:00
	if len(w.history) != 1 || w.history[0].Outcome != domain.OutcomeSkippedMissed || w.history[0].Count != 1 {
		t.Fatalf("skip task: %+v", w.history)
	}
	if len(w.started) != 1 || w.started[0] != "catchup@03-10 09:00" {
		t.Fatalf("catch-up task: %v", w.started)
	}
	w.at(at2(2026, 3, 10, 12, 1))
	if len(w.started) != 1 || len(w.history) != 1 {
		t.Fatal("nothing more until the next slot")
	}
}

func TestThreeDaysOffGivesOneCatchUp(t *testing.T) {
	it := task("pull", "daily 09:00", "catchup", t)
	w := newWorld(t, at2(2026, 3, 10, 8, 0), it)
	w.at(at2(2026, 3, 13, 10, 0))
	if len(w.started) != 1 || w.started[0] != "catchup@03-13 09:00" {
		t.Fatalf("one coalesced catch-up for the latest slot: %v", w.started)
	}
}

func TestTooOldMissesAreSkipped(t *testing.T) {
	it := task("pull", "daily 09:00", "catchup", t)
	it.Task.CatchUpMaxAgeHours = 2
	w := newWorld(t, at2(2026, 3, 10, 8, 0), it)
	w.at(at2(2026, 3, 10, 12, 0)) // the 09:00 slot is 3 h old
	if len(w.started) != 0 || len(w.history) != 1 || w.history[0].Outcome != domain.OutcomeSkippedMissed {
		t.Fatalf("started %v, history %+v", w.started, w.history)
	}
}

func TestOnTimeAndSleepJump(t *testing.T) {
	it := task("pull", "every 1h", "skip", t)
	w := newWorld(t, at2(2026, 1, 1, 0, 30), it)
	w.at(at2(2026, 1, 1, 1, 0)) // on time
	w.active["pull"] = false
	w.at(at2(2026, 1, 1, 6, 10)) // slept 5 h: slots 2..6 missed, the newest is 10 min old
	if len(w.started) != 1 || len(w.history) != 1 || w.history[0].Count != 5 {
		t.Fatalf("started %v, history %+v", w.started, w.history)
	}
	if next := w.s.Next("pull"); !next.Equal(at2(2026, 1, 1, 7, 0)) {
		t.Fatalf("next: %s", next)
	}
}

func TestNewAndReEnabledSchedulesNeverCatchUp(t *testing.T) {
	it := task("pull", "every 1h", "catchup", t)
	w := newWorld(t, at2(2026, 1, 1, 10, 30), it) // created at 10:30: the 10:00 slot is history
	if len(w.started) != 0 {
		t.Fatalf("a new schedule ran old slots: %v", w.started)
	}
	w.items[0].Task.Schedule.Enabled = false
	w.at(at2(2026, 1, 1, 11, 0))
	w.at(at2(2026, 1, 1, 15, 30)) // disabled through 4 slots
	w.items[0].Task.Schedule.Enabled = true
	w.at(at2(2026, 1, 1, 15, 31))
	if len(w.started) != 0 || len(w.history) != 0 {
		t.Fatalf("re-enabling must not run or skip old slots: %v %+v", w.started, w.history)
	}
	w.items[0].Task.Schedule = sched(t, "every 30m") // a changed schedule starts over too
	w.at(at2(2026, 1, 1, 15, 32))
	if len(w.started) != 0 {
		t.Fatalf("changed schedule: %v", w.started)
	}
	w.at(at2(2026, 1, 1, 16, 0))
	if len(w.started) != 1 {
		t.Fatalf("the next slot runs: %v", w.started)
	}
}

func TestOverlapAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		overlap string
		check   func(w *world)
	}{
		{"skip", func(w *world) {
			if len(w.history) != 1 || w.history[0].Outcome != domain.OutcomeSkippedOverlap || len(w.started) != 1 {
				t.Errorf("skip: %v %+v", w.started, w.history)
			}
		}},
		{"queue", func(w *world) {
			if len(w.started) != 1 {
				t.Errorf("queue must wait: %v", w.started)
			}
			w.active["job"] = false
			w.at(at2(2026, 1, 1, 2, 5))
			if len(w.started) != 2 || w.started[1] != "schedule@01-01 02:00" {
				t.Errorf("queued run after the first ended: %v", w.started)
			}
		}},
		{"restart", func(w *world) {
			if len(w.stopped) != 1 || len(w.started) != 2 {
				t.Errorf("restart: stopped %v started %v", w.stopped, w.started)
			}
		}},
	} {
		it := task("job", "every 1h", "skip", t)
		it.Task.Overlap = tc.overlap
		w := newWorld(t, at2(2026, 1, 1, 0, 30), it)
		w.at(at2(2026, 1, 1, 1, 0)) // starts, keeps running
		w.at(at2(2026, 1, 1, 2, 0)) // the next slot while it still runs
		tc.check(w)
	}

	it := task("slow", "every 1h", "skip", t)
	it.Task.TimeoutMinutes = 10
	w := newWorld(t, at2(2026, 1, 1, 0, 30), it)
	w.at(at2(2026, 1, 1, 1, 0))
	w.at(at2(2026, 1, 1, 1, 5))
	if len(w.stopped) != 0 {
		t.Fatal("stopped before its time limit")
	}
	w.at(at2(2026, 1, 1, 1, 11))
	if len(w.stopped) != 1 || w.stopped[0] != "slow:timeout" {
		t.Fatalf("time limit: %v", w.stopped)
	}
}

func TestStateSurvivesARestart(t *testing.T) {
	it := task("pull", "daily 09:00", "catchup", t)
	w := newWorld(t, at2(2026, 3, 10, 8, 0), it)
	w.s.Evaluate()
	home := w.s.home
	// a new scheduler on the same home, started after the slot: it catches up
	w2 := &world{t: t, clock: at2(2026, 3, 10, 11, 0), items: w.items, active: map[string]bool{}}
	w2.s = New(home, func() time.Time { return w2.clock }, Hooks{
		Items:  func() []domain.Item { return w2.items },
		Active: func(string) bool { return false },
		Start: func(id, trigger string, slot time.Time) error {
			w2.started = append(w2.started, trigger)
			return nil
		},
		Stop:   func(string, bool) {},
		Record: func(string, domain.HistoryLine) {},
	})
	w2.s.Evaluate()
	if len(w2.started) != 1 || w2.started[0] != domain.TriggerCatchup {
		t.Fatalf("after a restart: %v", w2.started)
	}
}
