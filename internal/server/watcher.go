package server

import (
	"context"
	"time"

	"github.com/mohamed-foly/orchdock/internal/runner"
)

// Poll intervals: quick while somebody listens to events, relaxed otherwise.
const (
	pollWithListeners    = time.Second
	pollWithoutListeners = 10 * time.Second
)

// watch notices changes nobody announced (a run that ended on its own, a host
// that died) and publishes them as events. It also writes the history line of
// every run that ended, so history stays current without anyone asking.
func (s *Server) watch(ctx context.Context) {
	for {
		s.pollOnce()
		wait := pollWithoutListeners
		if s.Hub.Count() > 0 {
			wait = pollWithListeners
		}
		select {
		case <-ctx.Done():
			return
		case <-s.kick: // somebody started listening: look now and then poll quickly
		case <-time.After(wait):
		}
	}
}

// pollOnce compares the state of every item with the last poll.
func (s *Server) pollOnce() {
	cat, err := s.Store.Load()
	if err != nil {
		return
	}
	now := s.Runner.Statuses(cat.Items) // also reconciles history
	s.mu.Lock()
	prev := s.prev
	s.prev = make(map[string]string, len(now))
	first := !s.polled
	s.polled = true
	var changes []func()
	for _, it := range cat.Items {
		st := now[it.ID]
		key := string(st.State) + "/" + st.RunID
		s.prev[it.ID] = key
		old, known := prev[it.ID]
		if first || (known && old == key) {
			continue
		}
		it, st, old := it, st, old
		changes = append(changes, func() {
			s.publish("item.changed", map[string]any{"itemId": it.ID, "change": "state"})
			switch {
			case st.State == runner.StateInterrupted:
				s.publish("run.exited", map[string]any{"itemId": it.ID, "runId": st.RunID, "reason": "interrupted"})
			case st.State == runner.StateRunning && old != "":
				s.publish("run.started", map[string]any{"itemId": it.ID, "runId": st.RunID})
			}
		})
	}
	s.mu.Unlock()
	for _, c := range changes {
		c()
	}
	if !first {
		s.watchServices(cat, now, prev)
		s.noticeFailures(cat, now, prev)
	}
	for _, it := range cat.Items { // follow every live run, also those started before this service
		if now[it.ID].State == runner.StateRunning {
			if run, ok := s.Runner.Active(it.ID); ok {
				s.watchRun(run)
			}
		}
	}
}
