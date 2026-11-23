package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func validService() Item {
	it := NewCatalog().NewItem(KindService, "Shop backlog board", "", now)
	it.Command.Line = "backlog browser --port {port} --no-open"
	it.Command.Cwd = "{projectDir}"
	it.Service.Port = 6421
	it.Service.PortRange = &[2]int{6420, 6499}
	it.Service.URL = "http://127.0.0.1:{port}/"
	it.Service.Health = &Health{Type: "http", URL: "http://127.0.0.1:{port}/", IntervalSec: 10, TimeoutSec: 3}
	return it
}

func TestNewItemDefaults(t *testing.T) {
	cat := NewCatalog()
	for _, k := range Kinds {
		it := cat.NewItem(k, "My "+string(k), "", now)
		if !ValidID(PrefixItem, it.ID) || it.Alias != "my-"+string(k) || it.Kind != k {
			t.Fatalf("%s: %+v", k, it)
		}
		switch k {
		case KindService:
			if it.Service == nil || it.Service.PortMode != "auto" || it.Service.Restart != "never" {
				t.Fatalf("service defaults: %+v", it.Service)
			}
		case KindTask:
			if it.Task == nil || it.Task.Missed != "skip" || it.Task.Overlap != "skip" || it.Task.CatchUpMaxAgeHours != 24 {
				t.Fatalf("task defaults: %+v", it.Task)
			}
		case KindAgent:
			if it.Interactive.StopSequence != "agent" || it.Agent != nil {
				t.Fatalf("agent defaults: %+v", it)
			}
		}
	}
	cat.Items = append(cat.Items, cat.NewItem(KindShell, "Shell", "", now))
	cat.AgentOverlays["ap_codex:x"] = AgentOverlay{Alias: "shell-2"}
	if a := cat.NewItem(KindShell, "Shell", "", now).Alias; a != "shell-3" {
		t.Fatalf("alias not de-duplicated against items and overlays: %q", a)
	}
	if a := cat.NewItem(KindShell, "Whatever", "Shell", now).Alias; a != "shell-3" {
		t.Fatalf("explicit alias not de-duplicated: %q", a)
	}
}

func TestItemValidate(t *testing.T) {
	task := func(mut func(*Item)) Item {
		it := NewCatalog().NewItem(KindTask, "Update", "", now)
		it.Command.Line = "git pull --ff-only"
		mut(&it)
		return it
	}
	svc := func(mut func(*Item)) Item {
		it := validService()
		mut(&it)
		return it
	}
	tests := []struct {
		name    string
		item    Item
		wantErr string
	}{
		{"valid task", task(func(*Item) {}), ""},
		{"valid service", svc(func(*Item) {}), ""},
		{"valid shell without command", NewCatalog().NewItem(KindShell, "Shell", "", now), ""},
		{"valid argv task", task(func(it *Item) { it.Command = Command{Shell: "none", Argv: []string{"git", "pull"}} }), ""},
		{"valid scheduled task", task(func(it *Item) {
			s, _ := ParseSpec("weekly mon,thu 09:00", now)
			it.Task.Schedule = &s
		}), ""},
		{"bad id", task(func(it *Item) { it.ID = "x_1" }), `id: "x_1" is not a valid id`},
		{"bad alias", task(func(it *Item) { it.Alias = "Bad Alias" }), `alias: "Bad Alias" must be`},
		{"missing title", task(func(it *Item) { it.Title = "" }), "title: is required"},
		{"bad kind", task(func(it *Item) { it.Kind = "robot" }), `kind: "robot" is not one of`},
		{"bad tag", task(func(it *Item) { it.Tags = []string{"Mixed Case"} }), "tags:"},
		{"unknown placeholder in line", task(func(it *Item) { it.Command.Line = "echo {foo}" }), "command.line: unknown placeholder {foo}"},
		{"unknown placeholder in cwd", task(func(it *Item) { it.Command.Cwd = "{projectdir}" }), "command.cwd: unknown placeholder {projectdir}"},
		{"unknown placeholder in env", task(func(it *Item) { it.Command.Env = map[string]string{"X": "{bar}"} }), "command.env.X: unknown placeholder {bar}"},
		{"line with shell none", task(func(it *Item) { it.Command.Shell = "none" }), `shell "none" runs argv directly`},
		{"argv without shell none", task(func(it *Item) { it.Command = Command{Shell: "auto", Argv: []string{"x"}} }), `argv needs shell "none"`},
		{"task without command", task(func(it *Item) { it.Command.Line = "" }), "a task needs a command"},
		{"bad shell", task(func(it *Item) { it.Command.Shell = "fish" }), `command.shell: "fish"`},
		{"duplicate variant ids", task(func(it *Item) { it.Command.Variants = []Variant{{ID: "a"}, {ID: "a"}} }), `command.variants[1].id: duplicate variant id "a"`},
		{"bad schedule", task(func(it *Item) { it.Task.Schedule = &Schedule{Kind: ScheduleDaily, Times: []string{"25:00"}} }), "task.schedule.times"},
		{"bad missed policy", task(func(it *Item) { it.Task.Missed = "sometimes" }), "task.missed"},
		{"service section on task", task(func(it *Item) { it.Service = &Service{} }), "only service items have a service section"},
		{"fixed port missing", svc(func(it *Item) { it.Service.PortMode, it.Service.Port = "fixed", 0 }), "service.port: is required when portMode is fixed"},
		{"port outside range", svc(func(it *Item) { it.Service.Port = 7000 }), "outside portRange"},
		{"bad range", svc(func(it *Item) { it.Service.PortRange = &[2]int{6499, 6420} }), "service.portRange"},
		{"bad health", svc(func(it *Item) { it.Service.Health.Type = "ping" }), "service.health.type"},
		{"bad restart", svc(func(it *Item) { it.Service.Restart = "sometimes" }), "service.restart"},
		{"agent without command or profile", NewCatalog().NewItem(KindAgent, "A", "", now), "an agent needs a command"},
		{"agent with a bad profile id", func() Item {
			it := NewCatalog().NewItem(KindAgent, "A", "", now)
			it.Agent = &Agent{ProfileID: "claude"}
			return it
		}(), `agent.profileId: "claude" is not a valid id`},
		{"link without target", NewCatalog().NewItem(KindLink, "L", "", now), "link.target: is required"},
		{"custom stop without text", task(func(it *Item) { it.Interactive.StopSequence = "custom" }), "interactive.stopText"},
		{"bad icon", task(func(it *Item) { it.Icon = Icon{Primary: "auto"} }), "icon.primary"},
		{"three badges", task(func(it *Item) { it.Icon.Badges = []string{"agent", "favicon", "project"} }), "at most 2 badges"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.item.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want ErrInvalid containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func validCatalog() Catalog {
	cat := NewCatalog()
	cat.Tags = append(cat.Tags, TagDef{Name: "shop", Path: "/srv/shop", Icon: Icon{Primary: "auto"}, Color: "#4f8cff"}, TagDef{Name: "backlog", Color: "#8b5cf6"})
	cat.AgentProfiles = append(cat.AgentProfiles,
		AgentProfile{ID: "ap_claude", Provider: "claude", Label: "Claude", Executable: "claude",
			ResumeArgs: []string{"--resume", "{sessionId}"}, NewArgs: []string{"--session-id", "{newSessionId}"}, NameArgs: []string{"--name", "{name}"},
			PromptArg: "positional", DefaultArgsPosition: "before", Badge: Badge{Text: "Cl", Color: "#d97757"}},
		AgentProfile{ID: "ap_codex", Provider: "codex", Label: "Codex", Executable: "codex",
			ResumeArgs: []string{"resume", "{sessionId}"}, PromptArg: "positional", DefaultArgsPosition: "after",
			Variants:       []Variant{{ID: "default", Label: "Default"}, {ID: "remote", Label: "Remote", AppendArgs: []string{"--remote", "ws://127.0.0.1:4810"}}},
			DefaultVariant: "default", Badge: Badge{Text: "Cx", Color: "#10a37f"}},
	)
	svc := validService()
	svc.Tags = append([]string{"shop"}, svc.Tags...)
	cat.Items = append(cat.Items, svc)
	ag := cat.NewItem(KindAgent, "Fix login", "", now)
	ag.Agent = &Agent{ProfileID: "ap_codex", SessionID: "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}
	ag.Command.DefaultVariant = "remote" // defined on the profile only
	cat.Items = append(cat.Items, ag)
	tk := cat.NewItem(KindTask, "Pull all", "", now)
	tk.Command.Line = "git pull --ff-only"
	tk.Task.Targets = []string{"shop"}
	cat.Items = append(cat.Items, tk)
	cat.Stacks = append(cat.Stacks, Stack{ID: "st_workday01", Name: "Workday", Entries: []StackEntry{{ItemID: svc.ID}, {ItemID: ag.ID, Variant: "remote"}}, StartDelaySec: 1})
	cat.Templates = append(cat.Templates, Template{Item: Item{ID: "t_backlog001", Alias: "{projectName}-backlog", Title: "Backlog board", Kind: KindService,
		Command: Command{Shell: "auto", Line: "backlog browser --port {port} --no-open"}, Service: &Service{PortMode: "auto", PortRange: &[2]int{6420, 6499}}},
		Params: []string{"port", "projectName"}})
	cat.Views = append(cat.Views, View{ID: "v_failing01", Name: "Failing", Query: "is:failed", Sort: "lastRun"})
	cat.AgentOverlays["ap_codex:0199"] = AgentOverlay{Alias: "side-quest", Tags: []string{"later"}, Hidden: true}
	return cat
}

func TestCatalogValidate(t *testing.T) {
	if err := validCatalog().Validate(); err != nil {
		t.Fatalf("valid catalog rejected: %v", err)
	}
	tests := []struct {
		name    string
		mut     func(*Catalog)
		wantErr string
	}{
		{"duplicate alias", func(c *Catalog) { c.Items[1].Alias = c.Items[0].Alias }, "alias: \"shop-backlog-board\" is already used by items[0]"},
		{"duplicate id", func(c *Catalog) { c.Items[1].ID = c.Items[0].ID }, "is used twice"},
		{"overlay alias clash", func(c *Catalog) { c.AgentOverlays["k"] = AgentOverlay{Alias: c.Items[0].Alias} }, "agentOverlays[k].alias"},
		{"missing profile", func(c *Catalog) { c.Items[1].Agent.ProfileID = "ap_gone" }, "agent profile \"ap_gone\" does not exist"},
		{"default variant not defined", func(c *Catalog) { c.Items[1].Command.DefaultVariant = "nope" }, "command.defaultVariant: \"nope\" is not one of"},
		{"bad target", func(c *Catalog) { c.Items[2].Task.Targets = []string{"Not A Tag"} }, "task.targets"},
		{"stack with missing item", func(c *Catalog) { c.Stacks[0].Entries[0].ItemID = "i_gone000001" }, "stacks[0].entries"},
		{"bad tag folder", func(c *Catalog) { c.Tags[0].Path = "relative/path" }, "tags[0].path"},
		{"bad profile provider", func(c *Catalog) { c.AgentProfiles[0].Provider = "other" }, "agentProfiles[0].provider"},
		{"profile default variant missing", func(c *Catalog) { c.AgentProfiles[1].DefaultVariant = "gone" }, "agentProfiles[1].defaultVariant"},
		{"template unknown placeholder", func(c *Catalog) { c.Templates[0].Command.Line = "x {what}" }, "templates[0].command.line: unknown placeholder {what}"},
		{"template unknown param", func(c *Catalog) { c.Templates[0].Params = []string{"what"} }, "templates[0].params"},
		{"bad tag colour", func(c *Catalog) { c.Tags[1].Color = "red" }, "tags[1].color"},
		{"bad view id", func(c *Catalog) { c.Views[0].ID = "x" }, "views[0].id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCatalog()
			tt.mut(&c)
			err := c.Validate()
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want ErrInvalid containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCatalogJSONRoundTrip(t *testing.T) {
	cat := validCatalog()
	b, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schemaVersion":1`, `"portMode":"auto"`, `"portRange":[6420,6499]`, `"profileId":"ap_codex"`, `"entries":[`, `"params":["port","projectName"]`, `"defaultArgsPosition":"after"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON lacks %s", key)
		}
	}
	var back Catalog
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(back)
	if string(b) != string(b2) {
		t.Fatalf("round trip changed the document:\n%s\n%s", b, b2)
	}
}

func TestRunSpecAndHistory(t *testing.T) {
	spec := RunSpec{RunID: NewID(PrefixRun), ItemID: NewID(PrefixItem), Trigger: TriggerManual, Variant: "remote",
		Argv: []string{"pwsh", "-NoLogo"}, Cwd: "/srv/shop", Env: []string{"A=1"}, Cols: 120, Rows: 32,
		ScrollbackBytes: 4 << 20, LogMaxBytes: 10 << 20, CreatedAt: now}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	adhoc := spec
	adhoc.ItemID = AdhocItemID
	if err := adhoc.Validate(); err != nil {
		t.Fatal(err)
	}
	badSpecs := map[string]func(*RunSpec){
		"trigger": func(s *RunSpec) { s.Trigger = "magic" },
		"argv":    func(s *RunSpec) { s.Argv = nil },
		"cwd":     func(s *RunSpec) { s.Cwd = "" },
		"cols":    func(s *RunSpec) { s.Cols = 0 },
		"runId":   func(s *RunSpec) { s.RunID = "x" },
		"variant": func(s *RunSpec) { s.Variant = "Bad Id" },
	}
	for field, mut := range badSpecs {
		s := spec
		mut(&s)
		if err := s.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: want ErrInvalid, got %v", field, err)
		}
	}

	zero := 0
	ok := HistoryLine{RunID: spec.RunID, Trigger: TriggerSchedule, ScheduledFor: now, StartedAt: now, EndedAt: now.Add(4210 * time.Millisecond),
		ExitCode: &zero, Outcome: OutcomeOK, DurationMs: 4210, Log: "runs/i_x/r_y/output.log"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ok)
	if !strings.Contains(string(b), `"exitCode":0`) {
		t.Fatalf("exit code 0 must be written: %s", b)
	}
	skipped := HistoryLine{Trigger: TriggerSchedule, ScheduledFor: now, Outcome: OutcomeSkippedMissed, Count: 3}
	if err := skipped.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]HistoryLine{
		"bad outcome":       {RunID: spec.RunID, Trigger: TriggerManual, Outcome: "meh"},
		"skipped with log":  {Trigger: TriggerSchedule, Outcome: OutcomeSkippedOverlap, Log: "x"},
		"run without runId": {Trigger: TriggerManual, Outcome: OutcomeFailed},
	} {
		if err := h.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}
