package stockroom

import (
	"context"
	"fmt"
	"time"
)

// When a loan is due (CLAUDE.md §7, decided 2026-09-26).
//
// The borrower picks the last day they will use the item, and it is due at the
// closing time (app_settings.due_time, 15:30 unless changed) on the next
// school day after that. "Due 23:59 on the day picked", the rule before this,
// made an item brought back first thing the next morning overdue by eight
// hours, and the overdue block then stopped that student borrowing anything.
//
// A school day is a weekday. Holidays are not known to the app; a loan whose
// due day falls on one is simply due that day.
//
// MaxCheckoutDays now caps the *last day of use*: it may be at most seven days
// after today, so the latest possible due time is the closing time on the
// first weekday after that. CLAUDE.md §13 records why the cap stopped being
// an exact instant.

// DefaultDueTime is the closing time a fresh install uses.
const DefaultDueTime = "15:30"

// parseDueTime reads "HH:MM" (24-hour). Both halves must be two digits, as
// the column's check constraint requires: time.Parse alone accepts "9:30",
// which the UPDATE would then refuse with a constraint name.
func parseDueTime(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 || s[2] != ':' {
		return 0, 0, fmt.Errorf("%w: the due time must be HH:MM on a 24-hour clock, such as 15:30", ErrInvalid)
	}
	return t.Hour(), t.Minute(), nil
}

// nextSchoolDayAt is hour:minute on the first weekday strictly after the date
// day falls on, in day's location.
func nextSchoolDayAt(day time.Time, hour, minute int) time.Time {
	y, m, d := day.Date()
	next := time.Date(y, m, d+1, hour, minute, 0, 0, day.Location())
	for next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
		next = time.Date(next.Year(), next.Month(), next.Day()+1, hour, minute, 0, 0, day.Location())
	}
	return next
}

// closingAtOrAfter is the first closing time on a school day at or after t,
// in t's location. A due time the picker computed is already one, so it comes
// back unchanged.
func closingAtOrAfter(t time.Time, hour, minute int) time.Time {
	y, m, d := t.Date()
	c := time.Date(y, m, d, hour, minute, 0, 0, t.Location())
	if c.Before(t) || c.Weekday() == time.Saturday || c.Weekday() == time.Sunday {
		return nextSchoolDayAt(c, hour, minute)
	}
	return c
}

// dueFor is when a loan is due whose borrower picked lastDay as the last day
// of use. The date picker computes the same thing in due.ts.
func dueFor(lastDay time.Time, dueTime string) (time.Time, error) {
	h, m, err := parseDueTime(dueTime)
	if err != nil {
		return time.Time{}, err
	}
	return nextSchoolDayAt(lastDay, h, m), nil
}

// latestDueAt is the latest due time a checkout made at now may have: the
// closing time after the last day of use MaxCheckoutDays out.
func latestDueAt(now time.Time, hour, minute int) time.Time {
	y, m, d := now.Date()
	return nextSchoolDayAt(time.Date(y, m, d+MaxCheckoutDays, 0, 0, 0, 0, now.Location()), hour, minute)
}

// dueTime reads the configured closing time, falling back to the default so a
// settings read that fails cannot stop a checkout.
func (db *DB) dueTime(ctx context.Context) string {
	var s string
	if err := db.Pool.QueryRow(ctx, `select due_time from app_settings where id = true`).Scan(&s); err != nil || s == "" {
		return DefaultDueTime
	}
	return s
}

// CheckoutRules is what the checkout screen needs to offer the same dates the
// server accepts: the cap on the last day of use, and the closing time.
type CheckoutRules struct {
	MaxCheckoutDays int    `json:"max_checkout_days"`
	DueTime         string `json:"due_time"`
}

// CheckoutRules reads the rules. Public: /signin/config carries them.
func (db *DB) CheckoutRules(ctx context.Context) CheckoutRules {
	return CheckoutRules{MaxCheckoutDays: MaxCheckoutDays, DueTime: db.dueTime(ctx)}
}
