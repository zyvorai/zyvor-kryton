// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones even on minimal container images
)

// Cron is a parsed 5-field cron expression: minute hour day-of-month month
// day-of-week. Fields accept "*", numbers, "a-b" ranges, "a,b" lists and
// "/n" steps; month and weekday also accept three-letter English names.
// Like Vixie cron, when both day-of-month and day-of-week are restricted a
// time matches if either does. Shortcuts: @hourly @daily @midnight @weekly
// @monthly @yearly.
type Cron struct {
	min, hour, dom, month, dow uint64
	domStar, dowStar           bool
	expr                       string
}

var shortcuts = map[string]string{
	"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@midnight": "0 0 * * *",
	"@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *", "@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *",
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// ParseCron parses a cron expression.
func ParseCron(expr string) (*Cron, error) {
	orig := strings.TrimSpace(expr)
	if s, ok := shortcuts[strings.ToLower(orig)]; ok {
		orig = s
	}
	f := strings.Fields(orig)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron expression needs 5 fields (minute hour day-of-month month day-of-week), got %d", len(f))
	}
	c := &Cron{expr: strings.TrimSpace(expr)}
	var err error
	if c.min, _, err = parseField(f[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if c.hour, _, err = parseField(f[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if c.dom, c.domStar, err = parseField(f[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	if c.month, _, err = parseField(f[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if c.dow, c.dowStar, err = parseField(f[4], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow |= 1
		c.dow &^= 1 << 7
	}
	return c, nil
}

func parseField(s string, lo, hi int, names map[string]int) (mask uint64, star bool, err error) {
	if s == "*" || s == "?" {
		for i := lo; i <= hi; i++ {
			mask |= 1 << uint(i)
		}
		return mask, true, nil
	}
	for _, part := range strings.Split(s, ",") {
		if part == "" {
			return 0, false, errors.New("empty list element")
		}
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			step, err = strconv.Atoi(stepStr)
			if err != nil || step < 1 {
				return 0, false, fmt.Errorf("invalid step %q", stepStr)
			}
		}
		a, b := lo, hi
		if rng != "*" {
			from, to, isRange := strings.Cut(rng, "-")
			if a, err = parseVal(from, names); err != nil {
				return 0, false, err
			}
			if isRange {
				if b, err = parseVal(to, names); err != nil {
					return 0, false, err
				}
			} else if hasStep {
				b = hi // "5/15" means 5-hi step 15
			} else {
				b = a
			}
		}
		if a < lo || b > hi || a > b {
			return 0, false, fmt.Errorf("value out of range %d-%d in %q", lo, hi, part)
		}
		for i := a; i <= b; i += step {
			mask |= 1 << uint(i)
		}
	}
	return mask, false, nil
}

func parseVal(s string, names map[string]int) (int, error) {
	if n, ok := names[strings.ToLower(s)]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	return n, nil
}

// String returns the original expression.
func (c *Cron) String() string { return c.expr }

func (c *Cron) dayMatches(t time.Time) bool {
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowOK
	case c.dowStar:
		return domOK
	default:
		return domOK || dowOK
	}
}

// Next returns the first matching minute strictly after t, evaluated in t's
// location (so DST transitions follow wall-clock time). The zero Time means
// no match within five years.
func (c *Cron) Next(t time.Time) time.Time {
	loc := t.Location()
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	// advance moves t to cand, but always forward (a DST gap can make
	// time.Date land earlier than t, which would otherwise loop forever).
	advance := func(cand time.Time) {
		if !cand.After(t) {
			cand = t.Add(time.Hour)
		}
		t = cand
	}
	for t.Before(limit) {
		if c.month&(1<<uint(t.Month())) == 0 {
			advance(time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc))
			continue
		}
		if !c.dayMatches(t) {
			advance(time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc))
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			t = t.Add(time.Hour - time.Duration(t.Minute())*time.Minute)
			continue
		}
		if c.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		// Fall-back DST repeats a wall-clock hour; fire only on its first pass.
		if p := t.Add(-time.Hour); p.Hour() == t.Hour() && p.Day() == t.Day() {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// LoadLocation resolves an IANA timezone name ("" means UTC).
func LoadLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q (use an IANA name such as Europe/Berlin)", name)
	}
	return loc, nil
}
