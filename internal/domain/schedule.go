package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule kinds.
const (
	ScheduleInterval = "interval" // every 15m, every 2h
	ScheduleDaily    = "daily"    // daily 09:00,18:30
	ScheduleWeekly   = "weekly"   // weekly mon,thu 09:00
	ScheduleMonthly  = "monthly"  // monthly 1 08:00 (day > month length = last day)
	ScheduleCron     = "cron"     // cron "*/5 * * * *" (5 fields)
)

// Weekdays in week order, as written in schedules.
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Schedule says when a task runs automatically (local time zone).
type Schedule struct {
	Enabled bool      `json:"enabled"`
	Kind    string    `json:"kind"`
	Every   string    `json:"every,omitempty"` // interval: "15m", "2h", "1h30m"
	Anchor  time.Time `json:"anchor,omitzero"` // interval: slots are anchor + k*every
	Times   []string  `json:"times,omitempty"` // daily: "HH:MM", sorted
	Days    []string  `json:"days,omitempty"`  // weekly: mon..sun, week order
	Time    string    `json:"time,omitempty"`  // weekly, monthly: "HH:MM"
	Day     int       `json:"day,omitempty"`   // monthly: 1..31
	Expr    string    `json:"expr,omitempty"`  // cron: 5 fields
}

var (
	everyRe = regexp.MustCompile(`^(?:(\d+)h)?(?:(\d+)m)?$`)
	clockRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
)

// Interval bounds.
const (
	MinInterval = time.Minute
	MaxInterval = 31 * 24 * time.Hour
)

// ParseEvery parses an interval such as "15m", "2h" or "1h30m".
func ParseEvery(s string) (time.Duration, error) {
	m := everyRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil || (m[1] == "" && m[2] == "") {
		return 0, invalid("every", "%q is not an interval like 15m, 2h or 1h30m", s)
	}
	h, _ := strconv.Atoi(m[1])
	mins, _ := strconv.Atoi(m[2])
	d := time.Duration(h)*time.Hour + time.Duration(mins)*time.Minute
	if d < MinInterval || d > MaxInterval {
		return 0, invalid("every", "%q must be between 1m and %dh", s, int(MaxInterval.Hours()))
	}
	return d, nil
}

// formatEvery prints d in the shortest h/m form.
func formatEvery(d time.Duration) string {
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// ParseClock parses "H:MM" or "HH:MM" (24 h) and returns it as "HH:MM".
func ParseClock(s string) (string, error) {
	m := clockRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", invalid("time", "%q is not a time like 09:00", s)
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	if h > 23 || mi > 59 {
		return "", invalid("time", "%q is not a valid 24-hour time", s)
	}
	return fmt.Sprintf("%02d:%02d", h, mi), nil
}

func parseClockList(s string) ([]string, error) {
	var out []string
	for _, p := range strings.Split(s, ",") {
		t, err := ParseClock(p)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out, nil
}

func parseDays(s string) ([]string, error) {
	var out []string
	for _, p := range strings.Split(strings.ToLower(s), ",") {
		p = strings.TrimSpace(p)
		if len(p) > 3 {
			p = p[:3] // monday → mon
		}
		if !slices.Contains(Weekdays, p) {
			return nil, invalid("days", "%q is not a weekday (mon, tue, wed, thu, fri, sat, sun)", p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(Weekdays, a) - slices.Index(Weekdays, b) })
	return out, nil
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseCron checks a 5-field cron expression (or a descriptor such as @daily).
func ParseCron(expr string) (cron.Schedule, error) {
	s, err := cronParser.Parse(strings.TrimSpace(expr))
	if err != nil {
		return nil, invalid("expr", "cron expression %q: %v", expr, err)
	}
	return s, nil
}

// ParseSpec parses a schedule spec string:
//
//	every 15m · every 2h · daily 09:00 · daily 09:00,18:30 ·
//	weekly mon,thu 09:00 · monthly 1 08:00 · cron "*/5 * * * *"
//
// The result is enabled; interval schedules get anchor (usually now).
func ParseSpec(spec string, anchor time.Time) (Schedule, error) {
	f := strings.Fields(spec)
	if len(f) == 0 {
		return Schedule{}, invalid("schedule", "empty schedule")
	}
	kind := strings.ToLower(f[0])
	args := f[1:]
	want := func(n int, usage string) error {
		if len(args) != n {
			return invalid("schedule", "%q: expected %q", spec, usage)
		}
		return nil
	}
	s := Schedule{Enabled: true}
	switch kind {
	case "every":
		if err := want(1, "every <15m|2h|1h30m>"); err != nil {
			return s, err
		}
		d, err := ParseEvery(args[0])
		if err != nil {
			return s, err
		}
		s.Kind, s.Every, s.Anchor = ScheduleInterval, formatEvery(d), anchor
	case ScheduleDaily:
		if err := want(1, "daily HH:MM[,HH:MM…]"); err != nil {
			return s, err
		}
		times, err := parseClockList(args[0])
		if err != nil {
			return s, err
		}
		s.Kind, s.Times = ScheduleDaily, times
	case ScheduleWeekly:
		if err := want(2, "weekly mon,thu HH:MM"); err != nil {
			return s, err
		}
		days, err := parseDays(args[0])
		if err != nil {
			return s, err
		}
		t, err := ParseClock(args[1])
		if err != nil {
			return s, err
		}
		s.Kind, s.Days, s.Time = ScheduleWeekly, days, t
	case ScheduleMonthly:
		if err := want(2, "monthly <1-31> HH:MM"); err != nil {
			return s, err
		}
		day, err := strconv.Atoi(args[0])
		if err != nil || day < 1 || day > 31 {
			return s, invalid("day", "%q is not a day of the month (1-31)", args[0])
		}
		t, err := ParseClock(args[1])
		if err != nil {
			return s, err
		}
		s.Kind, s.Day, s.Time = ScheduleMonthly, day, t
	case ScheduleCron:
		expr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(spec), f[0]))
		expr = strings.Trim(expr, `"'`)
		if _, err := ParseCron(expr); err != nil {
			return s, err
		}
		s.Kind, s.Expr = ScheduleCron, strings.Join(strings.Fields(expr), " ")
	default:
		return s, invalid("schedule", "%q: unknown kind %q (every, daily, weekly, monthly, cron)", spec, f[0])
	}
	return s, nil
}

// Spec formats the schedule back into its spec string.
func (s Schedule) Spec() string {
	switch s.Kind {
	case ScheduleInterval:
		return "every " + s.Every
	case ScheduleDaily:
		return "daily " + strings.Join(s.Times, ",")
	case ScheduleWeekly:
		return "weekly " + strings.Join(s.Days, ",") + " " + s.Time
	case ScheduleMonthly:
		return fmt.Sprintf("monthly %d %s", s.Day, s.Time)
	case ScheduleCron:
		return fmt.Sprintf("cron %q", s.Expr)
	}
	return ""
}

// Validate checks a stored schedule.
func (s Schedule) Validate() error {
	var c collector
	c.schedule("schedule", s)
	return c.err()
}

func (c *collector) schedule(field string, s Schedule) {
	sub := c.sub(field)
	defer c.merge(sub)
	clock := func(f, v string) {
		if t, err := ParseClock(v); err != nil || t != v {
			sub.addf(f, "%q is not a time like 09:00", v)
		}
	}
	switch s.Kind {
	case ScheduleInterval:
		if _, err := ParseEvery(s.Every); err != nil {
			sub.addf("every", "%q is not an interval between 1m and %dh", s.Every, int(MaxInterval.Hours()))
		}
	case ScheduleDaily:
		if len(s.Times) == 0 {
			sub.addf("times", "at least one time is required")
		}
		for _, t := range s.Times {
			clock("times", t)
		}
	case ScheduleWeekly:
		if len(s.Days) == 0 {
			sub.addf("days", "at least one weekday is required")
		}
		for _, d := range s.Days {
			if !slices.Contains(Weekdays, d) {
				sub.addf("days", "%q is not a weekday (mon … sun)", d)
			}
		}
		clock("time", s.Time)
	case ScheduleMonthly:
		if s.Day < 1 || s.Day > 31 {
			sub.addf("day", "%d is not a day of the month (1-31)", s.Day)
		}
		clock("time", s.Time)
	case ScheduleCron:
		if _, err := ParseCron(s.Expr); err != nil {
			sub.addf("expr", "%q is not a valid 5-field cron expression", s.Expr)
		}
	default:
		sub.addf("kind", "%q is not one of interval, daily, weekly, monthly, cron", s.Kind)
	}
}
