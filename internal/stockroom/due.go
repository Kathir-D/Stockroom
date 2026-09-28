package stockroom

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// When a checkout is due (CLAUDE.md §7, decided 2026-09-26).
//
// The borrower picks the last day they will use the item, and it is due at the
// closing time (app_settings.due_time, 15:30 unless changed) on the next
// school day after that. "Due 23:59 on the day picked", the rule before this,
// made an item brought back first thing the next morning overdue by eight
// hours, and the overdue block then stopped that student borrowing anything.
//
// A school day is a weekday that isn't one of the closed dates an admin keeps
// in Admin → Settings (app_settings.closed_dates), so a checkout ending before a
// holiday falls due on the first day back.
//
// MaxCheckoutDays caps the *last day of use*: by default it may be at most
// seven days after today (app_settings.max_checkout_days), so the latest
// possible due time is the closing time on the first school day after that.
// docs/decisions.md records why the cap stopped being an exact instant.

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

// checkoutRules is everything that decides a due date and whether a checkout
// may go ahead, read from app_settings once per checkout.
type checkoutRules struct {
	hour, minute int
	// maxDays caps the last day of use, in days after today.
	maxDays int
	// overdueBlocks is whether anything overdue refuses a new checkout.
	overdueBlocks bool
	// closed is the school's closed dates, as "2006-01-02".
	closed map[string]bool
}

// maxClosedRun bounds how far a due date may be pushed by closed dates, so a
// list that closes the whole year can't loop forever. A summer holiday is
// about sixty weekdays.
const maxClosedRun = 120

// defaultCheckoutRules is what a fresh install uses, and what a failed settings
// read falls back to so it can't stop a checkout.
func defaultCheckoutRules() checkoutRules {
	h, m, _ := parseDueTime(DefaultDueTime)
	return checkoutRules{hour: h, minute: m, maxDays: MaxCheckoutDays, overdueBlocks: true}
}

// schoolDay reports whether t's date is a weekday the school is open.
func (r checkoutRules) schoolDay(t time.Time) bool {
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		return false
	}
	return !r.closed[t.Format(time.DateOnly)]
}

// nextSchoolDayAt is the closing time on the first school day strictly after
// the date day falls on, in day's location.
func (r checkoutRules) nextSchoolDayAt(day time.Time) time.Time {
	y, m, d := day.Date()
	next := time.Date(y, m, d+1, r.hour, r.minute, 0, 0, day.Location())
	for i := 0; !r.schoolDay(next) && i < maxClosedRun; i++ {
		next = time.Date(next.Year(), next.Month(), next.Day()+1, r.hour, r.minute, 0, 0, day.Location())
	}
	return next
}

// closingAtOrAfter is the first closing time on a school day at or after t,
// in t's location. A due time the picker computed is already one, so it comes
// back unchanged.
func (r checkoutRules) closingAtOrAfter(t time.Time) time.Time {
	y, m, d := t.Date()
	c := time.Date(y, m, d, r.hour, r.minute, 0, 0, t.Location())
	if c.Before(t) || !r.schoolDay(c) {
		return r.nextSchoolDayAt(c)
	}
	return c
}

// dueFor is when a checkout is due whose borrower picked lastDay as the last day
// of use. The date picker computes the same thing in due.ts.
func (r checkoutRules) dueFor(lastDay time.Time) time.Time {
	return r.nextSchoolDayAt(lastDay)
}

// latestDueAt is the latest due time a checkout made at now may have: the
// closing time after the last day of use maxDays out.
func (r checkoutRules) latestDueAt(now time.Time) time.Time {
	y, m, d := now.Date()
	return r.nextSchoolDayAt(time.Date(y, m, d+r.maxDays, 0, 0, 0, 0, now.Location()))
}

// checkoutRules reads the rules, falling back to the defaults so a settings read
// that fails can't stop a checkout.
func (db *DB) checkoutRules(ctx context.Context) checkoutRules {
	r := defaultCheckoutRules()
	var dueTime string
	var closed []string
	err := db.Pool.QueryRow(ctx, `
		select due_time, max_checkout_days, overdue_blocks_checkout, closed_dates::text[]
		from app_settings where id = true`).Scan(&dueTime, &r.maxDays, &r.overdueBlocks, &closed)
	if err != nil {
		return defaultCheckoutRules()
	}
	if h, m, err := parseDueTime(dueTime); err == nil {
		r.hour, r.minute = h, m
	}
	r.closed = make(map[string]bool, len(closed))
	for _, d := range closed {
		r.closed[d] = true
	}
	return r
}

// CheckoutRules is what the checkout screen needs to offer the same dates the
// server accepts: the cap on the last day of use, the closing time and the
// closed dates, plus whether an overdue item blocks a checkout.
type CheckoutRules struct {
	MaxCheckoutDays       int      `json:"max_checkout_days"`
	DueTime               string   `json:"due_time"`
	ClosedDates           []string `json:"closed_dates"`
	OverdueBlocksCheckout bool     `json:"overdue_blocks_checkout"`
}

// CheckoutRules reads the rules. Public: /signin/config carries them.
func (db *DB) CheckoutRules(ctx context.Context) CheckoutRules {
	r := db.checkoutRules(ctx)
	closed := make([]string, 0, len(r.closed))
	for d := range r.closed {
		closed = append(closed, d)
	}
	slices.Sort(closed)
	return CheckoutRules{
		MaxCheckoutDays:       r.maxDays,
		DueTime:               fmt.Sprintf("%02d:%02d", r.hour, r.minute),
		ClosedDates:           closed,
		OverdueBlocksCheckout: r.overdueBlocks,
	}
}
