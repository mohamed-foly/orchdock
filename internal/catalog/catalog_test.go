package catalog

import (
	"strings"
	"time"

	"encoding/json"
	"errors"
	"fmt"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir(), nil)
}

func addItem(s *Store, alias string) error {
	return s.Update(func(f *File) error {
		it := f.NewItem(domain.KindTask, "Task "+alias, alias, time.Now())
		it.Command.Line = "echo " + alias
		f.Items = append(f.Items, it)
		return nil
	})
}

func itemIDs(t *testing.T, f File) []string {
	t.Helper()
	ids := make([]string, 0, len(f.Items))
	for _, it := range f.Items {
		ids = append(ids, it.Alias)
	}
	return ids
}

func TestLoadMissingIsEmptyWithAllKeys(t *testing.T) {
	s := newStore(t)
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.SchemaVersion != SchemaVersion || f.Items == nil || f.Tags == nil || f.AgentOverlays == nil {
		t.Fatalf("unexpected empty catalog: %+v", f)
	}
	if err := s.Update(func(*File) error { return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Home(), FileName))
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"schemaVersion", "tags", "kinds", "items", "agentProfiles", "agentOverlays", "templates", "stacks", "views", "trash"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("key %q missing from written file", k)
		}
	}
}

func TestUpdateWritesBackup(t *testing.T) {
	s := newStore(t)
	for _, a := range []string{"i-1", "i-2"} {
		if err := addItem(s, a); err != nil {
			t.Fatal(err)
		}
	}
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(t, f); len(got) != 2 || got[0] != "i-1" || got[1] != "i-2" {
		t.Fatalf("items %v", got)
	}
	if f.Items[0].Task == nil || f.Items[0].Task.Missed != "skip" {
		t.Fatalf("typed item not round-tripped: %+v", f.Items[0])
	}
	bak, err := readFile(filepath.Join(s.Home(), BackupName))
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(t, bak); len(got) != 1 || got[0] != "i-1" {
		t.Fatalf("backup should hold the previous version, got %v", got)
	}
	if _, err := os.Stat(filepath.Join(s.Home(), LockName)); !os.IsNotExist(err) {
		t.Fatalf("lock file left behind: %v", err)
	}
}

func TestInvalidCatalogIsNotWritten(t *testing.T) {
	s := newStore(t)
	if err := addItem(s, "ok"); err != nil {
		t.Fatal(err)
	}
	err := s.Update(func(f *File) error {
		it := f.NewItem(domain.KindTask, "Bad", "bad", time.Now())
		it.Command.Line = "run {nope}"
		f.Items = append(f.Items, it)
		return nil
	})
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "{nope}") {
		t.Fatalf("want ErrInvalid mentioning the placeholder, got %v", err)
	}
	f, _ := s.Load()
	if len(f.Items) != 1 {
		t.Fatalf("invalid catalog was written: %d items", len(f.Items))
	}
}

func TestUpdateErrorWritesNothing(t *testing.T) {
	s := newStore(t)
	boom := errors.New("boom")
	if err := s.Update(func(f *File) error {
		f.Items = append(f.Items, domain.Item{})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Home(), FileName)); !os.IsNotExist(err) {
		t.Fatal("file written despite error")
	}
}

func TestCorruptRecoversFromBackup(t *testing.T) {
	s := newStore(t)
	if err := addItem(s, "first"); err != nil {
		t.Fatal(err)
	}
	if err := addItem(s, "second"); err != nil { // backup now holds "first"
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Home(), FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(t, f); len(got) != 1 || got[0] != "first" {
		t.Fatalf("expected backup contents, got %v", got)
	}
	matches, _ := filepath.Glob(filepath.Join(s.Home(), "catalog.corrupt-*.json"))
	if len(matches) != 1 {
		t.Fatalf("corrupt file not preserved: %v", matches)
	}
}

func TestCorruptWithoutBackupIsEmpty(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(filepath.Join(s.Home(), FileName), []byte(`{"items": 5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := s.Load()
	if err != nil || len(f.Items) != 0 {
		t.Fatalf("want empty catalog, got %+v %v", f, err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(filepath.Join(s.Home(), FileName), []byte(`{"schemaVersion": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("want ErrNewerSchema, got %v", err)
	}
	if err := addItem(s, "x"); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("update must not overwrite a newer file, got %v", err)
	}
}

func TestConcurrentGoroutines(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := addItem(s, fmt.Sprintf("g%d", i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, _ := s.Load()
	if len(f.Items) != 20 {
		t.Fatalf("want 20 items, got %d", len(f.Items))
	}
}

// TestConcurrentProcesses runs 20 separate processes that each add an item.
func TestConcurrentProcesses(t *testing.T) {
	if home := os.Getenv("ORCHDOCK_CATALOG_CHILD_HOME"); home != "" {
		n, _ := strconv.Atoi(os.Getenv("ORCHDOCK_CATALOG_CHILD_N"))
		if err := addItem(New(home, nil), fmt.Sprintf("p%d", n)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	home := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(exe, "-test.run=^TestConcurrentProcesses$")
			cmd.Env = append(os.Environ(), "ORCHDOCK_CATALOG_CHILD_HOME="+home, "ORCHDOCK_CATALOG_CHILD_N="+strconv.Itoa(i))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("child %d: %v: %s", i, err, out)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	f, err := New(home, nil).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 20 {
		t.Fatalf("want 20 items after 20 processes, got %d", len(f.Items))
	}
}
