package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseSpec(t *testing.T) {
	anchor := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		spec string
		want Schedule
		back string
	}{
		{"every 15m", Schedule{Enabled: true, Kind: ScheduleInterval, Every: "15m", Anchor: anchor}, "every 15m"},
		{"every 2h", Schedule{Enabled: true, Kind: ScheduleInterval, Every: "2h", Anchor: anchor}, "every 2h"},
		{"every 90m", Schedule{Enabled: true, Kind: ScheduleInterval, Every: "1h30m", Anchor: anchor}, "every 1h30m"},
		{"daily 09:00", Schedule{Enabled: true, Kind: ScheduleDaily, Times: []string{"09:00"}}, "daily 09:00"},
		{"daily 18:30,9:00,09:00", Schedule{Enabled: true, Kind: ScheduleDaily, Times: []string{"09:00", "18:30"}}, "daily 09:00,18:30"},
		{"weekly thu,mon 09:00", Schedule{Enabled: true, Kind: ScheduleWeekly, Days: []string{"mon", "thu"}, Time: "09:00"}, "weekly mon,thu 09:00"},
		{"WEEKLY Monday 7:05", Schedule{Enabled: true, Kind: ScheduleWeekly, Days: []string{"mon"}, Time: "07:05"}, "weekly mon 07:05"},
		{"monthly 1 08:00", Schedule{Enabled: true, Kind: ScheduleMonthly, Day: 1, Time: "08:00"}, "monthly 1 08:00"},
		{"monthly 31 23:59", Schedule{Enabled: true, Kind: ScheduleMonthly, Day: 31, Time: "23:59"}, "monthly 31 23:59"},
		{`cron "*/5 * * * *"`, Schedule{Enabled: true, Kind: ScheduleCron, Expr: "*/5 * * * *"}, `cron "*/5 * * * *"`},
		{"cron 0 9 * * mon-fri", Schedule{Enabled: true, Kind: ScheduleCron, Expr: "0 9 * * mon-fri"}, `cron "0 9 * * mon-fri"`},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := ParseSpec(tt.spec, anchor)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("\n got %+v\nwant %+v", got, tt.want)
			}
			if got.Spec() != tt.back {
				t.Fatalf("Spec() = %q want %q", got.Spec(), tt.back)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("parsed schedule does not validate: %v", err)
			}
			again, err := ParseSpec(got.Spec(), anchor)
			if err != nil || !reflect.DeepEqual(again, got) {
				t.Fatalf("round trip: %+v %v", again, err)
			}
		})
	}
}

func TestParseSpecInvalid(t *testing.T) {
	tests := map[string]string{
		"":                     "empty schedule",
		"hourly":               `unknown kind "hourly"`,
		"every":                "expected",
		"every 0m":             "between 1m",
		"every 30s":            "not an interval",
		"every 2d":             "not an interval",
		"every 1000h":          "between 1m",
		"daily":                "expected",
		"daily 25:00":          "not a valid 24-hour time",
		"daily 9am":            "not a time like 09:00",
		"weekly mon":           "expected",
		"weekly funday 09:00":  "not a weekday",
		"weekly mon 9:60":      "not a valid 24-hour time",
		"monthly 0 08:00":      "not a day of the month",
		"monthly 32 08:00":     "not a day of the month",
		"monthly x 08:00":      "not a day of the month",
		`cron "* * *"`:         "cron expression",
		`cron "61 * * * *"`:    "cron expression",
		`cron "*/5 * * * * *"`: "cron expression",
	}
	for spec, want := range tests {
		t.Run(spec, func(t *testing.T) {
			_, err := ParseSpec(spec, time.Time{})
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
				t.Fatalf("want ErrInvalid containing %q, got %v", want, err)
			}
		})
	}
}

func TestScheduleValidateStored(t *testing.T) {
	bad := []Schedule{
		{Kind: "hourly"},
		{Kind: ScheduleInterval, Every: "5x"},
		{Kind: ScheduleDaily},
		{Kind: ScheduleDaily, Times: []string{"9:00"}}, // stored times are normalised HH:MM
		{Kind: ScheduleWeekly, Days: []string{"monday"}, Time: "09:00"},
		{Kind: ScheduleWeekly, Time: "09:00"},
		{Kind: ScheduleMonthly, Day: 40, Time: "09:00"},
		{Kind: ScheduleCron, Expr: "nope"},
	}
	for i, s := range bad {
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d (%+v): want ErrInvalid, got %v", i, s, err)
		}
	}
}
