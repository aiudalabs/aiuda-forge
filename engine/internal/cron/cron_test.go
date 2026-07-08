package cron

import (
	"testing"
	"time"
)

func TestParseInvalid(t *testing.T) {
	cases := []string{
		"",             // empty
		"0 13 * *",     // 4 fields
		"0 13 * * * *", // 6 fields
		"60 13 * * *",  // minute out of range
		"0 24 * * *",   // hour out of range
		"0 13 32 * *",  // day out of range
		"0 13 * 13 *",  // month out of range
		"0 13 * * 7",   // weekday out of range (0-6)
		"*/0 13 * * *", // zero step
		"0 5-1 * * *",  // inverted range
		"x 13 * * *",   // non-numeric
	}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q): expected error, got nil", c)
		}
	}
}

func TestParseValidAndNext(t *testing.T) {
	// The digest default: 13:00 UTC daily.
	s, err := Parse("0 13 * * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	after := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	next := s.Next(after)
	want := time.Date(2026, 7, 8, 13, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next = %v, want %v", next, want)
	}
	// After the fire time on the same day → next day.
	next2 := s.Next(time.Date(2026, 7, 8, 13, 0, 0, 0, time.UTC))
	want2 := time.Date(2026, 7, 9, 13, 0, 0, 0, time.UTC)
	if !next2.Equal(want2) {
		t.Fatalf("Next (rollover) = %v, want %v", next2, want2)
	}
}

func TestParseStepsAndLists(t *testing.T) {
	// Every 15 minutes.
	s, err := Parse("*/15 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	next := s.Next(time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC))
	if next.Minute() != 15 {
		t.Fatalf("*/15 Next minute = %d, want 15", next.Minute())
	}
	// A comma list of hours.
	s2, err := Parse("0 9,17 * * *")
	if err != nil {
		t.Fatal(err)
	}
	n := s2.Next(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
	if n.Hour() != 17 {
		t.Fatalf("9,17 Next hour = %d, want 17", n.Hour())
	}
}

func TestWeekdayMatch(t *testing.T) {
	// Mondays at 08:00 (Monday = 1).
	s, err := Parse("0 8 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-07-08 is a Wednesday; next Monday is 2026-07-13.
	next := s.Next(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	if next.Weekday() != time.Monday || next.Day() != 13 {
		t.Fatalf("Monday Next = %v, want 2026-07-13 Monday", next)
	}
}
