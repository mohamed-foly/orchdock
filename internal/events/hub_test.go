package events

import "testing"

func TestPublishSubscribeCancel(t *testing.T) {
	h := NewHub()
	a, cancelA := h.Subscribe()
	b, cancelB := h.Subscribe()
	if h.Count() != 2 {
		t.Fatalf("count %d", h.Count())
	}
	h.Publish(Event{Type: "run.started", Data: "r_1"})
	for _, ch := range []<-chan Event{a, b} {
		if e := <-ch; e.Type != "run.started" || e.Data != "r_1" {
			t.Fatalf("got %+v", e)
		}
	}
	cancelA()
	cancelA() // idempotent
	if _, ok := <-a; ok {
		t.Fatal("channel should be closed after cancel")
	}
	if h.Count() != 1 {
		t.Fatalf("count after cancel %d", h.Count())
	}
	cancelB()
}

func TestSlowSubscriberDropsInsteadOfBlocking(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()
	for i := 0; i < 1000; i++ {
		h.Publish(Event{Type: "tick", Data: i})
	}
	if n := len(ch); n != cap(ch) {
		t.Fatalf("buffer should be full, has %d of %d", n, cap(ch))
	}
	if e := <-ch; e.Data != 0 {
		t.Fatalf("oldest event should be kept first, got %v", e.Data)
	}
}
