package domain

import (
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

// Kind is the kind of an item.
type Kind string

// Item kinds.
const (
	KindAgent   Kind = "agent"
	KindService Kind = "service"
	KindTask    Kind = "task"
	KindShell   Kind = "shell"
	KindLink    Kind = "link"
)

// Kinds lists every item kind.
var Kinds = []Kind{KindAgent, KindService, KindTask, KindShell, KindLink}

// Enumerations used by items.
var (
	PortModes      = []string{"fixed", "auto"}
	HealthTypes    = []string{"tcp", "http"}
	RestartModes   = []string{"never", "on-failure", "always"}
	MissedPolicies = []string{"skip", "catchup"}
	OverlapModes   = []string{"skip", "queue", "restart"}
	AutoStarts     = []string{"never", "login", "restore"}
	StopSequences  = []string{"default", "agent", "custom"}
	Providers      = []string{"claude", "codex", "custom"}
	PromptArgs     = []string{"positional", "none"}
	ArgsPositions  = []string{"before", "after"}
)

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// ValidTag reports whether t is a lowercase tag of 1-32 characters.
func ValidTag(t string) bool { return tagRe.MatchString(t) }

// Project is a named folder with an icon, colour and tags.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Icon      Icon      `json:"icon,omitzero"`
	Color     string    `json:"color,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// Item is anything in the dock.
type Item struct {
	ID          string      `json:"id"`
	Alias       string      `json:"alias"`
	Title       string      `json:"title"`
	Kind        Kind        `json:"kind"`             // the behaviour: agent, service, task, shell or link
	KindID      string      `json:"kindId,omitempty"` // a user kind (KindDef) built on Kind, shown instead of it
	ProjectID   string      `json:"projectId,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Icon        Icon        `json:"icon,omitzero"`
	Notes       string      `json:"notes,omitempty"`
	Pinned      bool        `json:"pinned,omitempty"`
	Hidden      bool        `json:"hidden,omitempty"`
	Command     Command     `json:"command,omitzero"`
	Service     *Service    `json:"service,omitempty"`
	Task        *Task       `json:"task,omitempty"`
	Agent       *Agent      `json:"agent,omitempty"`
	Link        *Link       `json:"link,omitempty"`
	Interactive Interactive `json:"interactive,omitzero"`
	CreatedAt   time.Time   `json:"createdAt,omitzero"`
	UpdatedAt   time.Time   `json:"updatedAt,omitzero"`
	LastRunAt   time.Time   `json:"lastRunAt,omitzero"`
}

// Service holds the settings of a long-running item.
type Service struct {
	Port      int     `json:"port,omitempty"` // 0 = not assigned yet (auto)
	PortMode  string  `json:"portMode,omitempty"`
	PortRange *[2]int `json:"portRange,omitempty"` // nil = config.servicePortRange
	URL       string  `json:"url,omitempty"`
	Health    *Health `json:"health,omitempty"`
	Restart   string  `json:"restart,omitempty"`
}

// Health is a service health check.
type Health struct {
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	IntervalSec int    `json:"intervalSec,omitempty"`
	TimeoutSec  int    `json:"timeoutSec,omitempty"`
}

// Task holds the settings of a finite, schedulable item.
type Task struct {
	Schedule           *Schedule `json:"schedule,omitempty"`
	Missed             string    `json:"missed,omitempty"`
	CatchUpMaxAgeHours int       `json:"catchUpMaxAgeHours,omitempty"`
	Overlap            string    `json:"overlap,omitempty"`
	TimeoutMinutes     int       `json:"timeoutMinutes,omitempty"`
	Targets            []string  `json:"targets,omitempty"` // project ids: run once per project
}

// Agent links an item to an agent session.
type Agent struct {
	ProfileID    string    `json:"profileId"`
	SessionID    string    `json:"sessionId,omitempty"` // empty until a new session is linked
	ExtraArgs    []string  `json:"extraArgs,omitempty"`
	LastCwd      string    `json:"lastCwd,omitempty"`
	PendingSince time.Time `json:"pendingSince,omitzero"`
}

// Link is a folder, file or URL shortcut.
type Link struct {
	Target string `json:"target"`
}

// Interactive holds terminal behaviour shared by every kind.
type Interactive struct {
	KeepOpenOnExit    bool   `json:"keepOpenOnExit,omitempty"`
	AutoStart         string `json:"autoStart,omitempty"`
	QuietAttentionSec int    `json:"quietAttentionSec,omitempty"`
	StopSequence      string `json:"stopSequence,omitempty"`
	StopText          string `json:"stopText,omitempty"` // custom: sent before the default sequence
}

// Badge is an agent profile's monogram badge.
type Badge struct {
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

// AgentProfile says how to launch an agent provider.
type AgentProfile struct {
	ID                  string            `json:"id"`
	Provider            string            `json:"provider"`
	Label               string            `json:"label"`
	Executable          string            `json:"executable"`
	Home                string            `json:"home,omitempty"`
	Env                 map[string]string `json:"env,omitempty"`
	DefaultArgs         []string          `json:"defaultArgs,omitempty"`
	ResumeArgs          []string          `json:"resumeArgs,omitempty"`
	NewArgs             []string          `json:"newArgs,omitempty"`
	NameArgs            []string          `json:"nameArgs,omitempty"`
	PromptArg           string            `json:"promptArg,omitempty"`
	DefaultArgsPosition string            `json:"defaultArgsPosition,omitempty"`
	Variants            []Variant         `json:"variants,omitempty"`
	DefaultVariant      string            `json:"defaultVariant,omitempty"`
	Badge               Badge             `json:"badge,omitzero"`
}

// AgentOverlay is OrchDock metadata on a discovered session that is not an item yet.
type AgentOverlay struct {
	Alias  string   `json:"alias,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Notes  string   `json:"notes,omitempty"`
	Icon   Icon     `json:"icon,omitzero"`
	Pinned bool     `json:"pinned,omitempty"`
	Hidden bool     `json:"hidden,omitempty"`
}

// Template is a parameterised item definition (same shape as an item, plus
// the parameters it uses). Its alias may contain placeholders.
type Template struct {
	Item
	Params []string `json:"params,omitempty"`
}

// StackEntry is one item of a stack, with the variant to start it with.
type StackEntry struct {
	ItemID  string `json:"itemId"`
	Variant string `json:"variant,omitempty"`
}

// Stack is an ordered list of items started and stopped together.
type Stack struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Entries       []StackEntry `json:"entries"`
	StartDelaySec int          `json:"startDelaySec,omitempty"`
}

// View is a saved search.
type View struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Query string `json:"query"`
	Sort  string `json:"sort,omitempty"`
	Group string `json:"group,omitempty"` // project, kind or none
}

// SchemaVersion is the current catalog.json schema.
const SchemaVersion = 1

// Catalog is the content of catalog.json.
type Catalog struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Projects      []Project               `json:"projects,omitempty"` // older catalogs only: migrated to tags on load
	Tags          []TagDef                `json:"tags"`               // tag folders, colours and icons
	Kinds         []KindDef               `json:"kinds"`              // user kinds, and names or colours given to built-in ones
	Items         []Item                  `json:"items"`
	AgentProfiles []AgentProfile          `json:"agentProfiles"`
	AgentOverlays map[string]AgentOverlay `json:"agentOverlays"` // key "<profileId>:<sessionId>"
	Templates     []Template              `json:"templates"`
	Stacks        []Stack                 `json:"stacks"`
	TagColors     map[string]string       `json:"tagColors,omitempty"` // older catalogs only: migrated to tags on load
	Views         []View                  `json:"views"`
	Trash         []TrashedItem           `json:"trash"` // deleted items, restorable for TrashDays
}

// NewCatalog returns an empty catalog with every collection non-nil.
func NewCatalog() Catalog {
	var c Catalog
	c.Normalize()
	return c
}

// Normalize replaces nil collections so the file always has every key.
func (cat *Catalog) Normalize() {
	if cat.SchemaVersion == 0 {
		cat.SchemaVersion = SchemaVersion
	}
	if cat.Tags == nil {
		cat.Tags = []TagDef{}
	}
	if cat.Kinds == nil {
		cat.Kinds = []KindDef{}
	}
	if cat.Items == nil {
		cat.Items = []Item{}
	}
	if cat.AgentProfiles == nil {
		cat.AgentProfiles = []AgentProfile{}
	}
	if cat.AgentOverlays == nil {
		cat.AgentOverlays = map[string]AgentOverlay{}
	}
	if cat.Templates == nil {
		cat.Templates = []Template{}
	}
	if cat.Stacks == nil {
		cat.Stacks = []Stack{}
	}
	if cat.Views == nil {
		cat.Views = []View{}
	}
	if cat.Trash == nil {
		cat.Trash = []TrashedItem{}
	}
	cat.MigrateProjects() // older catalogs: projects become tags
}

func oneOf(c *collector, field, v string, allowed []string, required bool) {
	if v == "" && !required {
		return
	}
	if !slices.Contains(allowed, v) {
		c.addf(field, "%q is not one of %v", v, allowed)
	}
}

func (c *collector) tags(field string, tags []string) {
	for _, t := range tags {
		if !ValidTag(t) {
			c.addf(field, "%q must be 1-32 lowercase letters, digits, '.', '_' or '-'", t)
		}
	}
}

// Validate checks a project on its own.
func (p Project) Validate() error {
	var c collector
	checkID(&c, "id", PrefixProject, p.ID)
	if p.Name == "" {
		c.addf("name", "is required")
	}
	if p.Path != "" && !filepath.IsAbs(p.Path) && !isAbsAnyOS(p.Path) { // no folder: a plain group
		c.addf("path", "%q must be an absolute folder path", p.Path)
	}
	c.icon("icon", p.Icon, true)
	if p.Color != "" && !colorRe.MatchString(p.Color) {
		c.addf("color", "%q is not a colour like #4f8cff", p.Color)
	}
	c.tags("tags", p.Tags)
	return c.err()
}

// isAbsAnyOS accepts Windows drive/UNC paths and Unix paths on every OS, so a
// catalog stays valid when it is read on another platform.
func isAbsAnyOS(p string) bool {
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return len(p) > 1 && (p[:2] == `\\` || p[0] == '/')
}

// Validate checks an item on its own (references to other records are
// checked by Catalog.Validate).
func (it Item) Validate() error {
	var c collector
	it.validate(&c, false)
	return c.err()
}

func (it Item) validate(c *collector, template bool) {
	if !template {
		checkID(c, "id", PrefixItem, it.ID)
		if !ValidAlias(it.Alias) {
			c.add(CheckAlias(it.Alias))
		}
	} else {
		c.placeholders("alias", it.Alias)
	}
	if it.Title == "" {
		c.addf("title", "is required")
	}
	if !slices.Contains(Kinds, it.Kind) {
		c.addf("kind", "%q is not one of %v", it.Kind, Kinds)
		return
	}
	c.tags("tags", it.Tags)
	c.icon("icon", it.Icon, false)
	c.command("command", it.Kind, it.Command)
	section := func(name string, present bool, kind Kind) {
		if present && it.Kind != kind {
			c.addf(name, "only %s items have a %s section", kind, name)
		}
	}
	section("service", it.Service != nil, KindService)
	section("task", it.Task != nil, KindTask)
	section("agent", it.Agent != nil, KindAgent)
	section("link", it.Link != nil, KindLink)
	switch it.Kind {
	case KindService:
		if it.Service != nil {
			c.service(*it.Service)
		}
	case KindTask:
		if it.Task != nil {
			c.task(*it.Task)
		}
	case KindAgent:
		// An agent is a command like any other; a profile is optional and,
		// when set, supplies the command instead.
		hasProfile := it.Agent != nil && it.Agent.ProfileID != ""
		if hasProfile {
			checkID(c, "agent.profileId", PrefixProfile, it.Agent.ProfileID)
		}
		if it.Agent != nil {
			c.placeholders("agent.extraArgs", it.Agent.ExtraArgs...)
		}
		if !hasProfile && it.Command.Line == "" && len(it.Command.Argv) == 0 {
			c.addf("command", "an agent needs a command (line, or argv with shell \"none\")")
		}
	case KindLink:
		if it.Link == nil || it.Link.Target == "" {
			c.addf("link.target", "is required")
		} else {
			c.placeholders("link.target", it.Link.Target)
		}
	}
	in := it.Interactive
	oneOf(c, "interactive.autoStart", in.AutoStart, AutoStarts, false)
	oneOf(c, "interactive.stopSequence", in.StopSequence, StopSequences, false)
	if in.StopSequence == "custom" && in.StopText == "" {
		c.addf("interactive.stopText", "is required for the custom stop sequence")
	}
	if in.QuietAttentionSec < 0 {
		c.addf("interactive.quietAttentionSec", "must not be negative")
	}
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func (c *collector) service(s Service) {
	oneOf(c, "service.portMode", s.PortMode, PortModes, false)
	if s.Port != 0 && !validPort(s.Port) {
		c.addf("service.port", "%d is not a TCP port", s.Port)
	}
	if s.PortMode == "fixed" && s.Port == 0 {
		c.addf("service.port", "is required when portMode is fixed")
	}
	if r := s.PortRange; r != nil && (!validPort(r[0]) || !validPort(r[1]) || r[0] > r[1]) {
		c.addf("service.portRange", "%d-%d is not a valid port range", r[0], r[1])
	} else if r != nil && s.Port != 0 && s.PortMode == "auto" && (s.Port < r[0] || s.Port > r[1]) {
		c.addf("service.port", "%d is outside portRange %d-%d", s.Port, r[0], r[1])
	}
	c.placeholders("service.url", s.URL)
	if h := s.Health; h != nil {
		oneOf(c, "service.health.type", h.Type, HealthTypes, true)
		if h.Type == "http" && h.URL == "" && s.URL == "" {
			c.addf("service.health.url", "an http health check needs a url (or the service url)")
		}
		c.placeholders("service.health.url", h.URL)
		if h.IntervalSec < 0 || h.TimeoutSec < 0 {
			c.addf("service.health", "intervals must not be negative")
		}
	}
	oneOf(c, "service.restart", s.Restart, RestartModes, false)
}

func (c *collector) task(t Task) {
	if t.Schedule != nil {
		c.schedule("task.schedule", *t.Schedule)
	}
	oneOf(c, "task.missed", t.Missed, MissedPolicies, false)
	oneOf(c, "task.overlap", t.Overlap, OverlapModes, false)
	if t.CatchUpMaxAgeHours < 0 {
		c.addf("task.catchUpMaxAgeHours", "must not be negative")
	}
	if t.TimeoutMinutes < 0 {
		c.addf("task.timeoutMinutes", "must not be negative")
	}
	for _, p := range t.Targets {
		if !ValidTag(p) {
			c.addf("task.targets", "%q is not a tag", p)
		}
	}
}

// Validate checks an agent profile on its own.
func (p AgentProfile) Validate() error {
	var c collector
	checkID(&c, "id", PrefixProfile, p.ID)
	oneOf(&c, "provider", p.Provider, Providers, true)
	if p.Label == "" {
		c.addf("label", "is required")
	}
	if p.Executable == "" {
		c.addf("executable", "is required")
	}
	c.env("env", p.Env)
	c.placeholders("home", p.Home)
	c.placeholders("defaultArgs", p.DefaultArgs...)
	c.placeholders("resumeArgs", p.ResumeArgs...)
	c.placeholders("newArgs", p.NewArgs...)
	c.placeholders("nameArgs", p.NameArgs...)
	oneOf(&c, "promptArg", p.PromptArg, PromptArgs, false)
	oneOf(&c, "defaultArgsPosition", p.DefaultArgsPosition, ArgsPositions, false)
	c.variants("variants", p.Variants)
	if p.DefaultVariant != "" && !variantExists(MergeVariants(p.Variants, nil), p.DefaultVariant) {
		c.addf("defaultVariant", "%q is not one of the profile's variants", p.DefaultVariant)
	}
	if p.Badge.Color != "" && !colorRe.MatchString(p.Badge.Color) {
		c.addf("badge.color", "%q is not a colour like #d97757", p.Badge.Color)
	}
	return c.err()
}

func variantExists(vs []Variant, id string) bool {
	return slices.ContainsFunc(vs, func(v Variant) bool { return v.ID == id })
}

// Validate checks a template on its own.
func (t Template) Validate() error {
	var c collector
	checkID(&c, "id", PrefixTemplate, t.ID)
	t.validate(&c, true)
	for _, p := range t.Params {
		if !slices.Contains(Placeholders, p) {
			c.addf("params", "unknown parameter %q", p)
		}
	}
	return c.err()
}

// Validate checks the whole catalog: every record, unique ids and aliases,
// and references between records.
func (cat Catalog) Validate() error {
	var c collector
	ids := map[string]string{}
	unique := func(field, id string) {
		if prev, ok := ids[id]; ok {
			c.addf(field, "id %q is used twice (also %s)", id, prev)
		}
		ids[id] = field
	}
	profiles := map[string]AgentProfile{}
	for i, p := range cat.AgentProfiles {
		f := "agentProfiles[" + itoa(i) + "]"
		c.add(prefixed(f, p.Validate()))
		unique(f, p.ID)
		profiles[p.ID] = p
	}
	aliases := map[string]string{}
	items := map[string]bool{}
	for i, it := range cat.Items {
		f := "items[" + itoa(i) + "]"
		c.add(prefixed(f, it.Validate()))
		unique(f, it.ID)
		items[it.ID] = true
		if prev, ok := aliases[it.Alias]; ok {
			c.addf(f+".alias", "%q is already used by %s", it.Alias, prev)
		}
		aliases[it.Alias] = f
		var profileVariants []Variant
		if it.Agent != nil && it.Agent.ProfileID != "" {
			if p, ok := profiles[it.Agent.ProfileID]; ok {
				profileVariants = p.Variants
			} else {
				c.addf(f+".agent.profileId", "agent profile %q does not exist", it.Agent.ProfileID)
			}
		}
		if d := it.Command.DefaultVariant; d != "" && !variantExists(MergeVariants(profileVariants, it.Command.Variants), d) {
			c.addf(f+".command.defaultVariant", "%q is not one of the item's variants", d)
		}
	}
	for key, o := range cat.AgentOverlays {
		f := "agentOverlays[" + key + "]"
		if o.Alias != "" {
			if !ValidAlias(o.Alias) {
				c.addf(f+".alias", "%q is not a valid alias", o.Alias)
			} else if prev, ok := aliases[o.Alias]; ok {
				c.addf(f+".alias", "%q is already used by %s", o.Alias, prev)
			}
		}
		sub := c.sub(f)
		sub.tags("tags", o.Tags)
		sub.icon("icon", o.Icon, false)
		c.merge(sub)
	}
	for i, t := range cat.Templates {
		f := "templates[" + itoa(i) + "]"
		c.add(prefixed(f, t.Validate()))
		unique(f, t.ID)
	}
	for i, s := range cat.Stacks {
		f := "stacks[" + itoa(i) + "]"
		checkID(&c, f+".id", PrefixStack, s.ID)
		unique(f, s.ID)
		if s.Name == "" {
			c.addf(f+".name", "is required")
		}
		if s.StartDelaySec < 0 {
			c.addf(f+".startDelaySec", "must not be negative")
		}
		for _, e := range s.Entries {
			if !items[e.ItemID] {
				c.addf(f+".entries", "item %q does not exist", e.ItemID)
			}
			if e.Variant != "" && !ValidVariantID(e.Variant) {
				c.addf(f+".entries", "%q is not a valid variant id", e.Variant)
			}
		}
	}
	cat.validateKinds(&c)
	cat.validateTags(&c)
	for i, v := range cat.Views {
		f := "views[" + itoa(i) + "]"
		checkID(&c, f+".id", PrefixView, v.ID)
		unique(f, v.ID)
		if v.Name == "" {
			c.addf(f+".name", "is required")
		}
	}
	return c.err()
}

// AliasTaken reports whether alias is used by an item or an agent overlay.
func (cat Catalog) AliasTaken(alias string) bool {
	for _, it := range cat.Items {
		if it.Alias == alias {
			return true
		}
	}
	for _, o := range cat.AgentOverlays {
		if o.Alias == alias {
			return true
		}
	}
	return false
}

// NewItem returns an item of kind with a fresh id, an alias derived from the
// title (or from alias if given) that is unique in cat, the default icon and
// the default policies. It is not added to cat.
func (cat Catalog) NewItem(kind Kind, title, alias string, now time.Time) Item {
	base := alias
	if base == "" {
		base = DeriveAlias(title)
	}
	it := Item{
		ID:        NewID(PrefixItem),
		Alias:     UniqueAlias(base, cat.AliasTaken),
		Title:     title,
		Kind:      kind,
		Icon:      DefaultIcon(kind),
		Command:   Command{Shell: "auto"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	it.Interactive.AutoStart = "never"
	it.Interactive.StopSequence = "default"
	switch kind {
	case KindService:
		it.Service = &Service{PortMode: "auto", Restart: "never"}
	case KindTask:
		it.Task = &Task{Missed: "skip", CatchUpMaxAgeHours: 24, Overlap: "skip"}
	case KindAgent:
		it.Interactive.StopSequence = "agent"
	case KindLink:
		it.Link = &Link{}
		it.Command = Command{}
	}
	return it
}
