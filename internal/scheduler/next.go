// Package scheduler runs tasks on their schedules: it decides when a slot is
// due, what to do with slots missed while the computer was off or asleep, and
// what to do when the previous run is still going. It never reads the clock
// itself, so every rule can be tested with a fake clock.
package scheduler

import (
	"slices"
	"strconv"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// NextAfter returns the first slot of s strictly after t, in t's time zone.
// Slots are built from wall-clock dates, so daylight-saving changes behave
// like a clock on the wall: a time that does not exist that day moves
// forward, and a repeated time runs once. It returns the zero time when the
// schedule has no slot (a bad schedule).
func NextAfter(s domain.Schedule, t time.Time) time.Time {
	loc := t.Location()
	switch s.Kind {
	case domain.ScheduleInterval:
		d, err := domain.ParseEvery(s.Every)
		if err != nil {
			return time.Time{}
		}
		anchor := s.Anchor
		if anchor.IsZero() {
			anchor = t.Truncate(d)
		}
		if anchor.After(t) {
			return anchor.In(loc)
		}
		k := t.Sub(anchor)/d + 1
		return anchor.Add(k * d).In(loc)
	case domain.ScheduleDaily:
		y, m, d := t.Date()
		for day := 0; day <= 2; day++ {
			for _, hm := range s.Times {
				if c := at(y, m, d+day, hm, loc); c.After(t) {
					return c
				}
			}
		}
	case domain.ScheduleWeekly:
		y, m, d := t.Date()
		for day := 0; day <= 8; day++ {
			c := at(y, m, d+day, s.Time, loc)
			if slices.Contains(s.Days, weekday(c)) && c.After(t) {
				return c
			}
		}
	case domain.ScheduleMonthly:
		y, m, _ := t.Date()
		for k := 0; k <= 13; k++ {
			first := time.Date(y, m+time.Month(k), 1, 0, 0, 0, 0, loc)
			last := first.AddDate(0, 1, -1).Day()
			c := at(first.Year(), first.Month(), min(s.Day, last), s.Time, loc)
			if c.After(t) {
				return c
			}
		}
	case domain.ScheduleCron:
		cs, err := domain.ParseCron(s.Expr)
		if err != nil {
			return time.Time{}
		}
		return cs.Next(t)
	}
	return time.Time{}
}

// Missed counts the slots after `after` up to and including `upTo` and
// returns the latest one. It stops counting at limit.
func Missed(s domain.Schedule, after, upTo time.Time, limit int) (int, time.Time) {
	var latest time.Time
	n := 0
	for slot := NextAfter(s, after); !slot.IsZero() && !slot.After(upTo) && n < limit; slot = NextAfter(s, slot) {
		latest = slot
		n++
	}
	return n, latest
}

// at builds hh:mm on a calendar day. A time skipped by a daylight-saving
// change is normalised forward by time.Date.
func at(y int, m time.Month, d int, hm string, loc *time.Location) time.Time {
	h, _ := strconv.Atoi(hm[:2])
	mi, _ := strconv.Atoi(hm[3:])
	return time.Date(y, m, d, h, mi, 0, 0, loc)
}

func weekday(t time.Time) string { return domain.Weekdays[(int(t.Weekday())+6)%7] }
