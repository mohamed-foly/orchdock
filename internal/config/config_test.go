package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.UIPort != 7400 || c.ServicePortRange != [2]int{7401, 7499} {
		t.Fatalf("unexpected port defaults: %+v", c)
	}
	if c.Terminal.HostScrollbackBytes != 4<<20 || c.Terminal.LogMaxBytes != 10<<20 || c.Terminal.DetachKey != "ctrl+]" {
		t.Fatalf("unexpected terminal defaults: %+v", c.Terminal)
	}
	if c.History != (History{RunsPerItem: 50, LogsPerItem: 20, MaxAgeDays: 30}) || c.RestoreOnStart != "ask" || c.MaxConcurrentRuns != 64 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestSaveLoadRoundTripAndPartialFile(t *testing.T) {
	home := t.TempDir()
	c := Default()
	c.ServicePortRange = [2]int{8000, 8010}
	c.Theme = "dark"
	if err := Save(home, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home)
	if err != nil || got.ServicePortRange[0] != 8000 || got.Theme != "dark" || got.SchemaVersion != SchemaVersion {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	// A partial file keeps defaults for missing fields, including nested ones.
	if err := os.WriteFile(filepath.Join(home, FileName), []byte(`{"terminal":{"fontSize":15}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(home)
	if err != nil || got.Terminal.FontSize != 15 || got.Terminal.DetachKey != "ctrl+]" || got.UIPort != 7400 {
		t.Fatalf("partial: %+v %v", got, err)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":        `{not json`,
		"newer schema":  `{"schemaVersion":99}`,
		"invalid value": `{"theme":"neon"}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, FileName), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(home); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestValidate(t *testing.T) {
	bad := map[string]func(*Config){
		"ui port low":          func(c *Config) { c.UIPort = 80 },
		"ui port inside range": func(c *Config) { c.UIPort = 7450 },
		"range reversed":       func(c *Config) { c.ServicePortRange = [2]int{7499, 7401} },
		"theme":                func(c *Config) { c.Theme = "neon" },
		"shell":                func(c *Config) { c.DefaultShell = "fish" },
		"external":             func(c *Config) { c.Terminal.External = "xterm" },
		"custom without cmd":   func(c *Config) { c.Terminal.External = "custom" },
		"restore":              func(c *Config) { c.RestoreOnStart = "sometimes" },
		"font size":            func(c *Config) { c.Terminal.FontSize = 2 },
		"scrollback bytes":     func(c *Config) { c.Terminal.HostScrollbackBytes = 10 },
		"detach key":           func(c *Config) { c.Terminal.DetachKey = " " },
		"history":              func(c *Config) { c.History.RunsPerItem = 0 },
		"rescan":               func(c *Config) { c.Agents.RescanSeconds = 1 },
		"relative root":        func(c *Config) { c.ProjectRoots = []string{"relative/dir"} },
		"max runs":             func(c *Config) { c.MaxConcurrentRuns = 0 },
	}
	for name, mutate := range bad {
		c := Default()
		mutate(&c)
		if c.Validate() == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	c := Default()
	c.Terminal.External, c.Terminal.ExternalCustom = "custom", "term -e {attachCmd}"
	if err := c.Validate(); err != nil {
		t.Fatalf("custom terminal with command should be valid: %v", err)
	}
}
