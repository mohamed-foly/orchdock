package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/autostart"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/runner"
	"github.com/mohamed-foly/orchdock/internal/scheduler"
)

// startScheduler runs scheduled tasks while the service runs. Only the
// service schedules, so a task never runs twice for one slot.
func (s *Server) startScheduler(ctx context.Context) {
	s.sched = scheduler.New(s.Home, time.Now, scheduler.Hooks{
		Items: func() []domain.Item {
			cat, err := s.Store.Load()
			if err != nil {
				return nil
			}
			return cat.Items
		},
		Active: func(id string) bool { _, ok := s.Runner.Active(id); return ok },
		Start: func(id, trigger string, slot time.Time) error {
			run, err := s.Runner.Start(id, runner.StartOptions{Trigger: trigger, ScheduledFor: slot})
			if err == nil {
				s.runStarted(run)
			}
			return err
		},
		Stop: func(id string, timeout bool) {
			if run, err := s.Runner.Stop(id, runner.StopOptions{Timeout: timeout}); err == nil {
				s.runEnded(run)
			}
		},
		Record: func(id string, line domain.HistoryLine) {
			if s.Runner.Record(id, line) == nil {
				s.publish("item.changed", map[string]any{"itemId": id, "change": "history"})
			}
		},
	})
	go s.sched.Run(ctx)
	go func() { // a changed catalog may change schedules
		ch, cancel := s.Hub.Subscribe()
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.Type == "item.changed" {
					s.sched.Kick()
				}
			}
		}
	}()
}

// applyScheduleSpec handles "scheduleSpec" in an item body: a schedule as
// text ("daily 09:00", "every 2h", ...); "" removes it, "off" pauses it.
func applyScheduleSpec(body []byte, it *domain.Item, now time.Time) error {
	var in struct {
		Spec *string `json:"scheduleSpec"`
	}
	if json.Unmarshal(body, &in) != nil || in.Spec == nil {
		return nil
	}
	spec := strings.TrimSpace(*in.Spec)
	if it.Task == nil {
		if spec == "" {
			return nil
		}
		return fmt.Errorf("%w: only tasks can have a schedule", domain.ErrInvalid)
	}
	switch spec {
	case "":
		it.Task.Schedule = nil
	case "off":
		if it.Task.Schedule != nil {
			it.Task.Schedule.Enabled = false
		}
	default:
		if cur := it.Task.Schedule; cur != nil && cur.Spec() == spec {
			cur.Enabled = true // unchanged: keep its anchor
			return nil
		}
		s, err := domain.ParseSpec(spec, now)
		if err != nil {
			return err
		}
		it.Task.Schedule = &s
	}
	return nil
}

// schedulePreview answers ?spec=daily 09:00 with the next three slots, so
// the editor can show what a schedule means while it is typed.
func (s *Server) schedulePreview(w http.ResponseWriter, r *http.Request) error {
	now := time.Now()
	sched, err := domain.ParseSpec(r.URL.Query().Get("spec"), now)
	if err != nil {
		return err
	}
	var next []time.Time
	for t := now; len(next) < 3; {
		t = scheduler.NextAfter(sched, t)
		if t.IsZero() {
			break
		}
		next = append(next, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"spec": sched.Spec(), "next": next})
	return nil
}

// autostartGet says whether OrchDock starts at sign-in.
func (s *Server) autostartGet(w http.ResponseWriter, _ *http.Request) error {
	on, cmd, err := autostart.Enabled()
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"supported": autostart.Supported && s.AutostartExe != "", "enabled": on, "command": cmd})
	return nil
}

// autostartPut takes {"enabled": bool}.
func (s *Server) autostartPut(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if s.AutostartExe == "" {
		return errNotImplemented
	}
	if err := autostart.Set(in.Enabled, s.AutostartExe); err != nil {
		return err
	}
	return s.autostartGet(w, r)
}

// nextRun is when an item's schedule fires next (zero: not scheduled).
func (s *Server) nextRun(itemID string) time.Time {
	if s.sched == nil {
		return time.Time{}
	}
	return s.sched.Next(itemID)
}
