// Package catalog is the Repository over <home>/catalog.json.
//
// Every write is: take catalog.lock → read → modify → copy the current file to
// catalog.bak.json → write a temp file → rename → release the lock. A file that
// fails to parse is moved aside as catalog.corrupt-<time>.json and the backup is
// used instead.
//
// Update validates the whole catalog (domain rules) before writing, so an
// invalid catalog is never saved.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/fsutil"
)

// File names inside the home directory.
const (
	FileName   = "catalog.json"
	BackupName = "catalog.bak.json"
	LockName   = "catalog.lock"
)

// SchemaVersion is the current catalog.json schema.
const SchemaVersion = domain.SchemaVersion

const (
	lockTimeout = 10 * time.Second
	lockStale   = 10 * time.Second
)

// File is the on-disk document.
type File = domain.Catalog

// ErrNewerSchema means the file was written by a newer build.
var ErrNewerSchema = errors.New("catalog.json was written by a newer version")

// Store reads and writes the catalog of one home directory.
type Store struct {
	home string
	now  func() time.Time
}

// New returns a Store for home. now may be nil (uses time.Now).
func New(home string, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{home: home, now: now}
}

// Home returns the directory the store works in.
func (s *Store) Home() string { return s.home }

func (s *Store) path(name string) string { return filepath.Join(s.home, name) }

// Load reads the catalog without taking the lock (writes are atomic renames, so
// a reader always sees a complete file). A missing file gives an empty catalog;
// a corrupt one is moved aside and the backup is returned.
func (s *Store) Load() (File, error) {
	f, err := readFile(s.path(FileName))
	if err == nil {
		return f, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return domain.NewCatalog(), nil
	}
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	if errors.As(err, &syn) || errors.As(err, &typ) {
		corrupt := s.path(fmt.Sprintf("catalog.corrupt-%s.json", s.now().UTC().Format("20060102T150405.000Z")))
		_ = os.Rename(s.path(FileName), corrupt)
		if b, berr := readFile(s.path(BackupName)); berr == nil {
			return b, nil
		}
		return domain.NewCatalog(), nil
	}
	return File{}, err
}

func readFile(p string) (File, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, err
	}
	if f.SchemaVersion > SchemaVersion {
		return File{}, fmt.Errorf("%w (schemaVersion %d, supported %d)", ErrNewerSchema, f.SchemaVersion, SchemaVersion)
	}
	f.Normalize()
	return f, nil
}

// Update applies fn under the lock and saves. If fn returns an error nothing is written.
func (s *Store) Update(fn func(*File) error) error {
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	lock, err := fsutil.Lock(s.path(LockName), lockTimeout, lockStale)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	f, err := s.Load()
	if err != nil {
		return err
	}
	if err := fn(&f); err != nil {
		return err
	}
	if err := f.Validate(); err != nil {
		return err
	}
	return s.write(f)
}

func (s *Store) write(f File) error {
	f.SchemaVersion = SchemaVersion
	f.Normalize()
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if cur, err := os.ReadFile(s.path(FileName)); err == nil {
		_ = fsutil.WriteFileAtomic(s.path(BackupName), cur, 0o600)
	}
	return fsutil.WriteFileAtomic(s.path(FileName), append(b, '\n'), 0o600)
}
