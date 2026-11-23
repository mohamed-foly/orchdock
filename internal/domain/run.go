package domain

import "time"

// AdhocItemID is the item id used in run paths for runs without an item.
const AdhocItemID = "_adhoc"

// Run triggers.
const (
	TriggerManual        = "manual"
	TriggerSchedule      = "schedule"
	TriggerCatchup       = "catchup"
	TriggerRestore       = "restore"
	TriggerStack         = "stack"
	TriggerAPI           = "api"
	TriggerRestartPolicy = "restart-policy"
)

// Triggers lists every run trigger.
var Triggers = []string{TriggerManual, TriggerSchedule, TriggerCatchup, TriggerRestore, TriggerStack, TriggerAPI, TriggerRestartPolicy}

// Run outcomes recorded in history.
const (
	OutcomeOK             = "ok"
	OutcomeFailed         = "failed"
	OutcomeKilled         = "killed"
	OutcomeTimeout        = "timeout"
	OutcomeSkippedMissed  = "skipped-missed"
	OutcomeSkippedOverlap = "skipped-overlap"
	OutcomeStartFailed    = "start-failed"
	OutcomeInterrupted    = "interrupted"
)

// Outcomes lists every run outcome.
var Outcomes = []string{OutcomeOK, OutcomeFailed, OutcomeKilled, OutcomeTimeout, OutcomeSkippedMissed, OutcomeSkippedOverlap, OutcomeStartFailed, OutcomeInterrupted}

// RunSpec is runs/<itemId>/<runId>/spec.json: exactly what the host runs,
// written by the starter with everything expanded.
type RunSpec struct {
	RunID           string    `json:"runId"`
	ItemID          string    `json:"itemId"`
	Trigger         string    `json:"trigger"`
	Variant         string    `json:"variant,omitempty"`
	ScheduledFor    time.Time `json:"scheduledFor,omitzero"`
	Argv            []string  `json:"argv"`
	Cwd             string    `json:"cwd"`
	Env             []string  `json:"env"` // full environment, KEY=VALUE
	Cols            int       `json:"cols"`
	Rows            int       `json:"rows"`
	KeepOpenOnExit  bool      `json:"keepOpenOnExit"`
	ScrollbackBytes int       `json:"scrollbackBytes"`
	LogMaxBytes     int       `json:"logMaxBytes"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Validate checks a run spec before a host is started with it.
func (s RunSpec) Validate() error {
	var c collector
	checkID(&c, "runId", PrefixRun, s.RunID)
	if s.ItemID != AdhocItemID {
		checkID(&c, "itemId", PrefixItem, s.ItemID)
	}
	oneOf(&c, "trigger", s.Trigger, Triggers, true)
	if s.Variant != "" && !ValidVariantID(s.Variant) {
		c.addf("variant", "%q is not a valid variant id", s.Variant)
	}
	if len(s.Argv) == 0 || s.Argv[0] == "" {
		c.addf("argv", "the executable is required")
	}
	if s.Cwd == "" {
		c.addf("cwd", "is required")
	}
	if s.Cols < 1 || s.Rows < 1 {
		c.addf("cols", "terminal size %dx%d is invalid", s.Cols, s.Rows)
	}
	if s.ScrollbackBytes < 0 || s.LogMaxBytes < 0 {
		c.addf("scrollbackBytes", "sizes must not be negative")
	}
	return c.err()
}

// HistoryLine is one line of history/<itemId>.jsonl. Skipped runs have no log.
type HistoryLine struct {
	RunID        string    `json:"runId,omitempty"`
	Trigger      string    `json:"trigger"`
	Variant      string    `json:"variant,omitempty"`
	ScheduledFor time.Time `json:"scheduledFor,omitzero"`
	StartedAt    time.Time `json:"startedAt,omitzero"`
	EndedAt      time.Time `json:"endedAt,omitzero"`
	ExitCode     *int      `json:"exitCode,omitempty"`
	Outcome      string    `json:"outcome"`
	DurationMs   int64     `json:"durationMs,omitempty"`
	Log          string    `json:"log,omitempty"`   // relative to the home
	Count        int       `json:"count,omitempty"` // skipped-missed: how many slots were missed
}

// Validate checks a history line.
func (h HistoryLine) Validate() error {
	var c collector
	oneOf(&c, "trigger", h.Trigger, Triggers, true)
	oneOf(&c, "outcome", h.Outcome, Outcomes, true)
	skipped := h.Outcome == OutcomeSkippedMissed || h.Outcome == OutcomeSkippedOverlap
	if !skipped {
		checkID(&c, "runId", PrefixRun, h.RunID)
	}
	if skipped && h.Log != "" {
		c.addf("log", "skipped runs have no log")
	}
	if h.DurationMs < 0 || h.Count < 0 {
		c.addf("durationMs", "must not be negative")
	}
	return c.err()
}
