package server

import (
	"testing"
	"time"
)

func TestHealthStates(t *testing.T) {
	s := &Server{}
	if !s.setHealth("i", "r1", false) || s.healthOf("i") != HealthStarting {
		t.Fatalf("a new run starts as starting, got %q", s.healthOf("i"))
	}
	if s.setHealth("i", "r1", false) {
		t.Fatal("no change within the grace time")
	}
	if !s.setHealth("i", "r1", true) || s.healthOf("i") != HealthHealthy {
		t.Fatal("an answer makes it healthy")
	}
	if !s.setHealth("i", "r1", false) || s.healthOf("i") != HealthUnhealthy {
		t.Fatal("a healthy service that stops answering is unhealthy")
	}
	if !s.setHealth("i", "r2", false) || s.healthOf("i") != HealthStarting {
		t.Fatal("a new run starts over")
	}
	old := healthGrace
	healthGrace = 0
	defer func() { healthGrace = old }()
	time.Sleep(time.Millisecond)
	if !s.setHealth("i", "r2", false) || s.healthOf("i") != HealthUnhealthy {
		t.Fatal("never answering within the grace time is unhealthy")
	}
	s.forgetHealth("i")
	if s.healthOf("i") != "" {
		t.Fatal("forget")
	}
}
