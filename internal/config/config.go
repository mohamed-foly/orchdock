// Package config loads, validates and saves <home>/config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/fsutil"
)

// FileName is the config file inside the home directory.
const FileName = "config.json"

// SchemaVersion is the current config.json schema.
const SchemaVersion = 1

// Terminal holds terminal and run-output settings.
type Terminal struct {
	FontFamily          string `json:"fontFamily"`
	FontSize            int    `json:"fontSize"`
	ScrollbackLines     int    `json:"scrollbackLines"`     // xterm in the UI
	HostScrollbackBytes int    `json:"hostScrollbackBytes"` // ring buffer per run
	LogMaxBytes         int    `json:"logMaxBytes"`         // per-run log cap (keeps the tail)
	DetachKey           string `json:"detachKey"`
	External            string `json:"external"`       // see ExternalTerminals
	ExternalCustom      string `json:"externalCustom"` // e.g. "wezterm start -- {attachCmd}"
}

// History holds retention limits.
type History struct {
	RunsPerItem int `json:"runsPerItem"`
	LogsPerItem int `json:"logsPerItem"`
	MaxAgeDays  int `json:"maxAgeDays"`
}

// Agents holds agent discovery settings.
type Agents struct {
	ScanOnStart     bool `json:"scanOnStart"`
	RescanSeconds   int  `json:"rescanSeconds"` // while the UI is open
	IncludeArchived bool `json:"includeArchived"`
}

// Notifications selects which events raise a desktop notification.
type Notifications struct {
	TaskFailed      bool `json:"taskFailed"`
	ServiceCrashed  bool `json:"serviceCrashed"`
	ScheduleSkipped bool `json:"scheduleSkipped"`
	Attention       bool `json:"attention"`
}

// Attention holds attention-signal settings.
type Attention struct {
	Bell                bool `json:"bell"`
	QuietSecondsDefault int  `json:"quietSecondsDefault"`
}

// Config is the user-editable configuration.
type Config struct {
	SchemaVersion     int           `json:"schemaVersion"`
	UIPort            int           `json:"uiPort"`
	ServicePortRange  [2]int        `json:"servicePortRange"`
	Theme             string        `json:"theme"`
	Terminal          Terminal      `json:"terminal"`
	DefaultShell      string        `json:"defaultShell"`
	History           History       `json:"history"`
	Agents            Agents        `json:"agents"`
	ProjectRoots      []string      `json:"projectRoots"`
	Notifications     Notifications `json:"notifications"`
	Attention         Attention     `json:"attention"`
	RestoreOnStart    string        `json:"restoreOnStart"`
	MaxConcurrentRuns int           `json:"maxConcurrentRuns"`
}

// Allowed enum values.
var (
	Themes            = []string{"system", "light", "dark"}
	Shells            = []string{"auto", "pwsh", "powershell", "cmd", "bash", "zsh", "sh"}
	ExternalTerminals = []string{"auto", "windows-terminal", "conhost", "macos-terminal", "gnome-terminal", "x-terminal-emulator", "custom"}
	RestoreModes      = []string{"ask", "always", "never"}
)

// Default returns the decided defaults.
func Default() Config {
	return Config{
		SchemaVersion:    SchemaVersion,
		UIPort:           7400,
		ServicePortRange: [2]int{7401, 7499},
		Theme:            "system",
		Terminal: Terminal{
			FontFamily:          "Cascadia Mono, Consolas, Menlo, monospace",
			FontSize:            13,
			ScrollbackLines:     5000,
			HostScrollbackBytes: 4 << 20,
			LogMaxBytes:         10 << 20,
			DetachKey:           "ctrl+]",
			External:            "auto",
		},
		DefaultShell:      "auto",
		History:           History{RunsPerItem: 50, LogsPerItem: 20, MaxAgeDays: 30},
		Agents:            Agents{ScanOnStart: true, RescanSeconds: 30},
		ProjectRoots:      []string{},
		Notifications:     Notifications{TaskFailed: true, ServiceCrashed: true, Attention: true},
		Attention:         Attention{Bell: true},
		RestoreOnStart:    "ask",
		MaxConcurrentRuns: 64,
	}
}

// Validate checks values that would make the tool misbehave.
func (c Config) Validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !validPort(c.UIPort) {
		bad("uiPort %d out of range 1024-65535", c.UIPort)
	}
	lo, hi := c.ServicePortRange[0], c.ServicePortRange[1]
	if !validPort(lo) || !validPort(hi) || lo > hi {
		bad("servicePortRange %d-%d invalid", lo, hi)
	} else if c.UIPort >= lo && c.UIPort <= hi {
		bad("uiPort %d must be outside servicePortRange %d-%d", c.UIPort, lo, hi)
	}
	oneOf := func(field, v string, allowed []string) {
		if !slices.Contains(allowed, v) {
			bad("%s must be one of %s, got %q", field, strings.Join(allowed, " | "), v)
		}
	}
	oneOf("theme", c.Theme, Themes)
	oneOf("defaultShell", c.DefaultShell, Shells)
	oneOf("terminal.external", c.Terminal.External, ExternalTerminals)
	oneOf("restoreOnStart", c.RestoreOnStart, RestoreModes)
	if c.Terminal.External == "custom" && strings.TrimSpace(c.Terminal.ExternalCustom) == "" {
		bad("terminal.externalCustom is required when terminal.external is custom")
	}
	if c.Terminal.FontSize < 6 || c.Terminal.FontSize > 72 {
		bad("terminal.fontSize %d out of range 6-72", c.Terminal.FontSize)
	}
	if c.Terminal.ScrollbackLines < 0 {
		bad("terminal.scrollbackLines must not be negative")
	}
	if c.Terminal.HostScrollbackBytes < 64<<10 {
		bad("terminal.hostScrollbackBytes must be at least 65536")
	}
	if c.Terminal.LogMaxBytes < 64<<10 {
		bad("terminal.logMaxBytes must be at least 65536")
	}
	if strings.TrimSpace(c.Terminal.DetachKey) == "" {
		bad("terminal.detachKey must not be empty")
	}
	if c.History.RunsPerItem < 1 || c.History.LogsPerItem < 0 || c.History.MaxAgeDays < 1 {
		bad("history limits invalid: runsPerItem>=1, logsPerItem>=0, maxAgeDays>=1")
	}
	if c.Agents.RescanSeconds < 5 {
		bad("agents.rescanSeconds must be at least 5")
	}
	for _, r := range c.ProjectRoots {
		if !filepath.IsAbs(r) {
			bad("projectRoots entry %q must be an absolute path", r)
		}
	}
	if c.Attention.QuietSecondsDefault < 0 {
		bad("attention.quietSecondsDefault must not be negative")
	}
	if c.MaxConcurrentRuns < 1 {
		bad("maxConcurrentRuns must be at least 1")
	}
	return errors.Join(errs...)
}

func validPort(p int) bool { return p >= 1024 && p <= 65535 }

// Load reads <home>/config.json, filling missing fields with defaults.
func Load(home string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(filepath.Join(home, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("config.json: %w", err)
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	if c.SchemaVersion > SchemaVersion {
		return c, fmt.Errorf("config.json schemaVersion %d is newer than this build supports (%d)", c.SchemaVersion, SchemaVersion)
	}
	return c, c.Validate()
}

// Save validates and writes the config atomically.
func Save(home string, c Config) error {
	c.SchemaVersion = SchemaVersion
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(home, FileName), append(b, '\n'), 0o600)
}
