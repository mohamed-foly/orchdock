// Package host is the small per-run process that owns a run's pseudo-terminal.
//
// Every run (agent, service, task or shell: they are all just commands) gets
// its own host. The host starts the command in a PTY, keeps recent output,
// writes a capped log, and lets any number of viewers attach and detach over
// loopback TCP. It keeps running when the service, the UI or the CLI exit, so
// work is never lost because a window closed.
package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// Files inside a run directory.
const (
	SpecFile    = "spec.json"   // written by the starter: what to run
	InfoFile    = "host.json"   // written by the host while it is alive
	ResultFile  = "result.json" // written by the host when the command ends
	LogFile     = "output.log"  // raw terminal output, capped
	HostLogFile = "host.log"    // the host's own diagnostics
)

// Defaults for sizes that a spec leaves at zero.
const (
	DefaultScrollbackBytes = 4 << 20
	DefaultLogMaxBytes     = 10 << 20
)

// Info is host.json: how to reach a live host.
type Info struct {
	PID       int       `json:"pid"`
	ChildPID  int       `json:"childPid"`
	Port      int       `json:"port"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"startedAt"`
	InJob     bool      `json:"hostInJob,omitempty"` // diagnostic (Windows): the host itself started inside a job
}

// Result is result.json: how the command ended.
type Result struct {
	ExitCode int       `json:"exitCode"`
	EndedAt  time.Time `json:"endedAt"`
	Reason   string    `json:"reason"` // exited | killed
}

// Reasons recorded in a Result.
const (
	ReasonExited = "exited"
	ReasonKilled = "killed"
)

// ReadSpec loads and validates dir/spec.json.
func ReadSpec(dir string) (domain.RunSpec, error) {
	var s domain.RunSpec
	if err := readJSON(filepath.Join(dir, SpecFile), &s); err != nil {
		return s, err
	}
	if err := s.Validate(); err != nil {
		return s, fmt.Errorf("%s: %w", SpecFile, err)
	}
	return s, nil
}

// ReadInfo loads dir/host.json.
func ReadInfo(dir string) (Info, error) {
	var i Info
	err := readJSON(filepath.Join(dir, InfoFile), &i)
	if err == nil && (i.Port == 0 || i.Token == "") {
		return i, fmt.Errorf("%s is incomplete", InfoFile)
	}
	return i, err
}

// ReadResult loads dir/result.json.
func ReadResult(dir string) (Result, error) {
	var r Result
	err := readJSON(filepath.Join(dir, ResultFile), &r)
	return r, err
}

// utf8BOM is the byte order mark that Windows PowerShell 5 puts at the start of
// UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b = bytes.TrimPrefix(b, utf8BOM) // Windows tools often write a UTF-8 BOM
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}
