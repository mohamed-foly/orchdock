package ports

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestAllocateSkipsReservedAndBusy(t *testing.T) {
	busy := map[int]bool{7402: true}
	got, err := Allocate(7401, 7409, map[int]bool{7401: true}, func(p int) bool { return !busy[p] })
	if err != nil || got != 7403 {
		t.Fatalf("got %d %v, want 7403", got, err)
	}
	if _, err := Allocate(7401, 7402, map[int]bool{7401: true}, func(p int) bool { return !busy[p] }); !errors.Is(err, ErrExhausted) {
		t.Fatalf("want exhausted, got %v", err)
	}
}

func TestListeningAndBindable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	p := l.Addr().(*net.TCPAddr).Port
	if !IsListening(p) || IsBindable(p) || IsFree(p) {
		t.Fatalf("port %d should be busy", p)
	}
	l.Close()
	if IsListening(p) || !IsFree(p) {
		t.Fatalf("port %d should be free after close", p)
	}
}

func TestHolderNamesThisProcess(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	got := Holder(port)
	if runtime.GOOS != "windows" {
		if got != "" {
			t.Fatalf("Holder on %s: %q", runtime.GOOS, got)
		}
		return
	}
	if !strings.Contains(got, fmt.Sprintf("pid %d", os.Getpid())) {
		t.Fatalf("Holder(%d) = %q, want this process (pid %d)", port, got, os.Getpid())
	}
	l.Close()
	if got := Holder(port); got != "" {
		t.Fatalf("Holder of a closed port = %q", got)
	}
}
