package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/runner"
)

// Health states of a running service.
const (
	HealthStarting  = "starting"  // not answered yet, still within the grace time
	HealthHealthy   = "healthy"   // answered the last check
	HealthUnhealthy = "unhealthy" // stopped answering, or never answered in time
)

// Restart policy limits: back off 1, 2, 4, 8 s; give up after this many
// restarts within the window and mark the item as crashed.
const (
	restartLimit  = 5
	restartWindow = 10 * time.Minute
)

// healthGrace is how long a service may take to answer for the first time.
var healthGrace = 60 * time.Second

type health struct {
	runID string
	state string
	since time.Time
}

// serviceWatch is the service-specific part of the watcher. Its zero value is ready.
type serviceWatch struct {
	mu       sync.Mutex
	health   map[string]health      // item id -> health of its current run
	restarts map[string][]time.Time // item id -> recent automatic restarts
	fetching map[string]bool        // item id -> its web icon is being fetched
}

// healthOf returns the health of an item's running service ("" when unknown).
func (s *Server) healthOf(itemID string) string {
	s.svc.mu.Lock()
	defer s.svc.mu.Unlock()
	return s.svc.health[itemID].state
}

// watchServices runs after every poll: it checks the health of running
// services and applies restart policies to services that ended by themselves.
func (s *Server) watchServices(cat catalog.File, now map[string]runner.Status, prev map[string]string) {
	type probe struct {
		it  domain.Item
		run string
		ok  bool
	}
	var probes []*probe
	for _, it := range cat.Items {
		if it.Service == nil {
			continue
		}
		st := now[it.ID]
		if st.State == runner.StateRunning {
			if target(it) != "" {
				probes = append(probes, &probe{it: it, run: st.RunID})
			}
			continue
		}
		s.forgetHealth(it.ID)
		if old := prev[it.ID]; strings.HasPrefix(old, string(runner.StateRunning)+"/") {
			s.maybeRestart(it)
		}
	}
	var wg sync.WaitGroup
	for _, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.ok = check(target(p.it))
		}()
	}
	wg.Wait()
	for _, p := range probes {
		if p.ok {
			s.maybeFetchFavicon(p.it) // the service answers: its own icon can be read
		}
		if s.setHealth(p.it.ID, p.run, p.ok) {
			s.publish("item.changed", map[string]any{"itemId": p.it.ID, "change": "health"})
		}
	}
}

// target is what to check: "http://..." or "tcp:port"; "" when the service
// has neither a port nor an http health check.
func target(it domain.Item) string {
	sv := it.Service
	port := strconv.Itoa(sv.Port)
	if h := sv.Health; h != nil && h.Type == "http" {
		u := h.URL
		if u == "" {
			u = sv.URL
		}
		return strings.ReplaceAll(u, "{port}", port)
	}
	if sv.Port > 0 {
		return "tcp:" + port
	}
	return ""
}

func check(t string) bool {
	if port, ok := strings.CutPrefix(t, "tcp:"); ok {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}

// setHealth records one check and reports whether the state changed.
func (s *Server) setHealth(itemID, runID string, ok bool) bool {
	s.svc.mu.Lock()
	defer s.svc.mu.Unlock()
	if s.svc.health == nil {
		s.svc.health = map[string]health{}
	}
	h := s.svc.health[itemID]
	if h.runID != runID {
		h = health{runID: runID, state: HealthStarting, since: time.Now()}
	}
	next := h.state
	switch {
	case ok:
		next = HealthHealthy
	case h.state == HealthHealthy:
		next = HealthUnhealthy
	case h.state == HealthStarting && time.Since(h.since) > healthGrace:
		next = HealthUnhealthy
	}
	changed := next != h.state || s.svc.health[itemID].runID != runID
	h.state = next
	s.svc.health[itemID] = h
	return changed
}

func (s *Server) forgetHealth(itemID string) {
	s.svc.mu.Lock()
	defer s.svc.mu.Unlock()
	delete(s.svc.health, itemID)
}

// maybeRestart applies the restart policy to a service whose run just
// ended. A run the user stopped ends as "killed" and is never restarted.
func (s *Server) maybeRestart(it domain.Item) {
	policy := it.Service.Restart
	if policy != "on-failure" && policy != "always" {
		return
	}
	hist, _ := s.Runner.History(it.ID)
	if len(hist) == 0 {
		return
	}
	switch last := hist[len(hist)-1].Outcome; {
	case last == domain.OutcomeFailed:
	case last == domain.OutcomeOK && policy == "always":
	default:
		return
	}
	s.svc.mu.Lock()
	if s.svc.restarts == nil {
		s.svc.restarts = map[string][]time.Time{}
	}
	var recent []time.Time
	for _, t := range s.svc.restarts[it.ID] {
		if time.Since(t) < restartWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= restartLimit {
		s.svc.restarts[it.ID] = recent
		s.svc.mu.Unlock()
		reason := fmt.Sprintf("crashed: gave up after %d restarts in %s", restartLimit, restartWindow)
		s.mu.Lock()
		s.attention[it.ID] = reason
		s.mu.Unlock()
		s.publish("attention", map[string]any{"itemId": it.ID, "reason": reason})
		return
	}
	delay := time.Duration(1<<len(recent)) * time.Second
	s.svc.restarts[it.ID] = append(recent, time.Now())
	s.svc.mu.Unlock()
	time.AfterFunc(delay, func() {
		run, err := s.Runner.Start(it.ID, runner.StartOptions{Trigger: domain.TriggerRestartPolicy})
		if err == nil {
			s.runStarted(run)
		}
	})
}
