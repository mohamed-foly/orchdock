package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/runner"
	"github.com/mohamed-foly/orchdock/internal/wire"
)

// Attention is how a run asks for the user: a terminal bell (an agent waiting
// for an answer), a failure, or `orchdock notify`. It stays on the item until
// the user opens it, and it is announced as an "attention" event, which the
// window shows and the tray turns into a notification.

// watchRun follows a run's attention signals through a quiet monitor
// connection (it gets no screen output). One watcher per run.
func (s *Server) watchRun(run runner.Run) {
	s.mu.Lock()
	if s.monitors[run.ID] {
		s.mu.Unlock()
		return
	}
	s.monitors[run.ID] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.monitors, run.ID)
			s.mu.Unlock()
		}()
		info, err := host.ReadInfo(run.Dir)
		if err != nil {
			return
		}
		conn, err := host.Dial(info, wire.HelloMsg{Client: wire.ClientMonitor})
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			f, err := conn.R.Next()
			if err != nil {
				return // the run ended or the host went away
			}
			if f.Type != wire.Attention {
				continue
			}
			var m wire.AttentionMsg
			if f.JSON(&m) != nil {
				continue
			}
			reason := "waiting for you"
			if m.Reason != "" && m.Reason != "bell" {
				reason = m.Reason
			}
			s.raiseAttention(run.ItemID, reason)
		}
	}()
}

// raiseAttention marks an item and announces it.
func (s *Server) raiseAttention(itemID, reason string) {
	s.mu.Lock()
	s.attention[itemID] = reason
	s.mu.Unlock()
	alias := itemID
	if cat, err := s.Store.Load(); err == nil {
		if it, err := runner.FindItem(cat.Items, itemID); err == nil {
			alias = it.Alias
		}
	}
	s.publish("attention", map[string]any{"itemId": itemID, "alias": alias, "reason": reason})
	s.publish("item.changed", map[string]any{"itemId": itemID, "change": "attention"})
}

// AttentionCount is how many items want the user (for the tray).
func (s *Server) AttentionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.attention)
}

// noticeFailures raises attention for runs that ended badly by themselves.
// A run the user stopped ends as "killed" and raises nothing.
func (s *Server) noticeFailures(cat catalog.File, now map[string]runner.Status, prev map[string]string) {
	for _, it := range cat.Items {
		old := prev[it.ID]
		if !strings.HasPrefix(old, string(runner.StateRunning)+"/") || now[it.ID].State == runner.StateRunning {
			continue
		}
		if it.Service != nil && it.Service.Restart != "" && it.Service.Restart != "never" {
			continue // its restart policy handles it, and raises attention when it gives up
		}
		hist, _ := s.Runner.History(it.ID)
		if len(hist) == 0 {
			continue
		}
		last := hist[len(hist)-1]
		switch last.Outcome {
		case domain.OutcomeFailed:
			reason := "failed"
			if last.ExitCode != nil {
				reason = fmt.Sprintf("failed (exit %d)", *last.ExitCode)
			}
			s.raiseAttention(it.ID, reason)
		case domain.OutcomeTimeout:
			s.raiseAttention(it.ID, "stopped: it ran past its time limit")
		case domain.OutcomeInterrupted:
			s.raiseAttention(it.ID, "its terminal went away")
		}
	}
}

// attentionRaise takes {"reason": "..."}: scripts call it through `orchdock notify`.
func (s *Server) attentionRaise(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	cat, err := s.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, r.PathValue("ref"))
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "asked for you"
	}
	if len(reason) > 200 {
		reason = reason[:200]
	}
	s.raiseAttention(it.ID, reason)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
