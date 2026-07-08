// Package cron is a tiny, dependency-free parser for the classic 5-field cron
// expression (minute hour day-of-month month day-of-week). It exists so the
// control plane can schedule the daily digest without pulling in an external
// cron dependency (kernel rule: no deps without cause). It supports the common
// syntaxes — `*`, a single number, comma lists (`1,15`), ranges (`1-5`) and
// steps (`*/5`, `0-30/10`) — which covers every schedule the digest needs. It is
// deliberately NOT a full crontab implementation: no `@daily` macros, no
// named months/weekdays, no seconds field.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression. Each field is the set of matching values.
type Schedule struct {
	spec    string
	minute  map[int]bool // 0-59
	hour    map[int]bool // 0-23
	dom     map[int]bool // 1-31
	month   map[int]bool // 1-12
	dow     map[int]bool // 0-6 (Sunday=0)
	domStar bool         // true when day-of-month field was "*"
	dowStar bool         // true when day-of-week field was "*"
}

// String returns the original spec.
func (s Schedule) String() string { return s.spec }

type fieldSpec struct {
	name     string
	min, max int
}

var fields = []fieldSpec{
	{"minute", 0, 59},
	{"hour", 0, 23},
	{"day-of-month", 1, 31},
	{"month", 1, 12},
	{"day-of-week", 0, 6},
}

// Parse parses a 5-field cron expression. It returns a clear error naming the
// offending field so a bad VIBEFORGE_DIGEST_CRON fails the boot loudly instead
// of silently never firing.
func Parse(spec string) (Schedule, error) {
	parts := strings.Fields(strings.TrimSpace(spec))
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("cron %q: want 5 space-separated fields (minute hour day month weekday), got %d", spec, len(parts))
	}
	sets := make([]map[int]bool, 5)
	for i, f := range fields {
		set, err := parseField(parts[i], f.min, f.max)
		if err != nil {
			return Schedule{}, fmt.Errorf("cron %q: %s field: %w", spec, f.name, err)
		}
		sets[i] = set
	}
	return Schedule{
		spec:    strings.TrimSpace(spec),
		minute:  sets[0],
		hour:    sets[1],
		dom:     sets[2],
		month:   sets[3],
		dow:     sets[4],
		domStar: parts[2] == "*",
		dowStar: parts[4] == "*",
	}, nil
}

// parseField expands one field (which may be a comma list of terms) into the set
// of integers it matches, bounded to [min,max].
func parseField(field string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, term := range strings.Split(field, ",") {
		if term == "" {
			return nil, fmt.Errorf("empty term in %q", field)
		}
		// Optional step: base/step.
		step := 1
		base := term
		if slash := strings.IndexByte(term, '/'); slash >= 0 {
			base = term[:slash]
			n, err := strconv.Atoi(term[slash+1:])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("bad step in %q", term)
			}
			step = n
		}
		lo, hi := min, max
		switch {
		case base == "*":
			// full range with the step applied
		case strings.IndexByte(base, '-') >= 0:
			dash := strings.IndexByte(base, '-')
			var err error
			if lo, err = strconv.Atoi(base[:dash]); err != nil {
				return nil, fmt.Errorf("bad range start in %q", term)
			}
			if hi, err = strconv.Atoi(base[dash+1:]); err != nil {
				return nil, fmt.Errorf("bad range end in %q", term)
			}
		default:
			n, err := strconv.Atoi(base)
			if err != nil {
				return nil, fmt.Errorf("bad value %q", base)
			}
			lo, hi = n, n
		}
		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("value %q out of range [%d,%d]", term, min, max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}

// matches reports whether t falls on the schedule. Per POSIX cron, when BOTH
// day-of-month and day-of-week are restricted (neither is "*"), a match on
// EITHER counts; otherwise both must match.
func (s Schedule) matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	domOK := s.dom[t.Day()]
	dowOK := s.dow[int(t.Weekday())]
	if !s.domStar && !s.dowStar {
		return domOK || dowOK
	}
	return domOK && dowOK
}

// Next returns the earliest time strictly after `after` that matches the
// schedule, evaluated in after's location. It scans minute-by-minute with a
// bounded horizon (4 years) so an impossible schedule (e.g. Feb 30) returns the
// zero time rather than looping forever.
func (s Schedule) Next(after time.Time) time.Time {
	// Advance to the next whole minute (cron has minute resolution).
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(4, 0, 0)
	for t.Before(limit) {
		if s.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}
