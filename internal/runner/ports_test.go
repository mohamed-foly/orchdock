package runner

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/ports"
)

// freeRange finds n consecutive free ports.
func freeRange(t *testing.T, n int) [2]int {
	t.Helper()
	for lo := 47100; lo < 48000; lo += n {
		ok := true
		for p := lo; p < lo+n && ok; p++ {
			ok = ports.IsFree(p)
		}
		if ok {
			return [2]int{lo, lo + n - 1}
		}
	}
	t.Fatal("no free port range")
	return [2]int{}
}

func TestAutoPortIsAssignedOnceAndKept(t *testing.T) {
	r := newRunner(t)
	rng := freeRange(t, 5)
	svc := func(alias string) {
		addItem(t, r, domain.KindService, alias, "listen", "{port}", func(it *domain.Item) {
			it.Service = &domain.Service{PortMode: "auto", PortRange: &rng, Restart: "never"}
		})
	}
	svc("one")
	svc("two")
	run, err := r.Start("one", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first := r.item(t, "one").Service.Port
	if first < rng[0] || first > rng[1] {
		t.Fatalf("port %d outside %v", first, rng)
	}
	output(t, run, fmt.Sprintf("listening:%d", first))
	if _, err := r.Start("two", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if second := r.item(t, "two").Service.Port; second == first || second < rng[0] || second > rng[1] {
		t.Fatalf("second service got %d (first %d)", second, first)
	}
	if _, err := r.Stop("one", StopOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	run, err = r.Start("one", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if again := r.item(t, "one").Service.Port; again != first {
		t.Fatalf("the port changed between starts: %d then %d", first, again)
	}
	output(t, run, fmt.Sprintf("listening:%d", first))
}

func TestAPortInUseIsRefusedAndNamed(t *testing.T) {
	r := newRunner(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	addItem(t, r, domain.KindService, "busy", "listen", "{port}", func(it *domain.Item) {
		it.Service = &domain.Service{PortMode: "fixed", Port: port, Restart: "never"}
	})
	_, err = r.Start("busy", StartOptions{})
	if !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), fmt.Sprint(port)) {
		t.Fatalf("want a conflict naming the port, got %v", err)
	}
	if want := fmt.Sprintf("pid %d", os.Getpid()); ports.Holder(port) != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("the error should name the holder (%s): %v", want, err)
	}
	if r.item(t, "busy").Service.Port != port {
		t.Fatal("the port must never move")
	}
}

func TestATimedOutRunIsRecordedAsTimeout(t *testing.T) {
	r := newRunner(t)
	addItem(t, r, domain.KindTask, "slow", "sleep", "", nil)
	if _, err := r.Start("slow", StartOptions{Trigger: domain.TriggerSchedule}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Stop("slow", StopOptions{Force: true, Timeout: true}); err != nil {
		t.Fatal(err)
	}
	hist, _ := r.History(r.item(t, "slow").ID)
	if len(hist) != 1 || hist[0].Outcome != domain.OutcomeTimeout || hist[0].Trigger != domain.TriggerSchedule {
		t.Fatalf("history: %+v", hist)
	}
}
