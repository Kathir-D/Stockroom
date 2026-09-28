package stockroom

import (
	"errors"
	"testing"
	"time"
)

// A loan is due at the closing time on the next weekday after the last day
// of use: Friday's goes to Monday, and a day picked on a weekend too.
func TestDueFor(t *testing.T) {
	loc := time.FixedZone("school", -5*3600)
	cases := map[string]string{
		"2026-09-28": "2026-09-29 15:30", // Monday -> Tuesday
		"2026-10-02": "2026-10-05 15:30", // Friday -> Monday
		"2026-10-03": "2026-10-05 15:30", // Saturday -> Monday
		"2026-10-04": "2026-10-05 15:30", // Sunday -> Monday
	}
	for day, want := range cases {
		d, _ := time.ParseInLocation("2006-01-02", day, loc)
		got := defaultLoanRules().dueFor(d)
		if got.Format("2006-01-02 15:04") != want {
			t.Errorf("dueFor(%s) = %s, want %s", day, got.Format("2006-01-02 15:04"), want)
		}
	}
	if _, _, err := parseDueTime("3:30pm"); !errors.Is(err, ErrInvalid) {
		t.Errorf("parseDueTime(3:30pm) = %v, want ErrInvalid", err)
	}
}

// The cap is on the last day of use: seven days out is allowed, with its due
// time on the school day after, and anything later is not.
func TestCheckDueAtCapsTheLastDayOfUse(t *testing.T) {
	loc := time.FixedZone("school", 0)
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, loc) // a Monday
	lastDay := now.AddDate(0, 0, MaxCheckoutDays)   // next Monday
	r := defaultLoanRules()
	due := r.dueFor(lastDay) // next Tuesday 15:30
	if got, err := checkDueAt(due, now, r); err != nil || !got.Equal(due) {
		t.Errorf("due after the seventh day = %v, %v; want it unchanged", got, err)
	}
	if _, err := checkDueAt(due.Add(time.Minute), now, r); !errors.Is(err, ErrInvalid) {
		t.Errorf("a minute past the cap = %v, want ErrInvalid", err)
	}
	if _, err := checkDueAt(now.Add(-time.Minute), now, r); !errors.Is(err, ErrInvalid) {
		t.Errorf("a due time in the past = %v, want ErrInvalid", err)
	}
	// Picking today means due the next school day, never already overdue.
	today := r.dueFor(now)
	if _, err := checkDueAt(today, now, r); err != nil || !today.After(now.Add(24*time.Hour)) {
		t.Errorf("due for today's last day = %v (%v), want tomorrow afternoon", today, err)
	}
}

// The server holds the rule, not the picker: any instant is moved forward to
// the first closing time on a school day at or after it.
func TestCheckDueAtMovesToAClosingTime(t *testing.T) {
	loc := time.FixedZone("school", 0)
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, loc) // a Monday
	cases := map[string]string{
		"2026-10-03 02:00": "2026-10-05 15:30", // Saturday night -> Monday
		"2026-09-29 09:00": "2026-09-29 15:30", // Tuesday morning -> that afternoon
		"2026-09-29 16:00": "2026-09-30 15:30", // Tuesday after closing -> Wednesday
		"2026-10-02 18:00": "2026-10-05 15:30", // Friday evening -> Monday
	}
	for in, want := range cases {
		at, _ := time.ParseInLocation("2006-01-02 15:04", in, loc)
		got, err := checkDueAt(at, now, defaultLoanRules())
		if err != nil || got.Format("2006-01-02 15:04") != want {
			t.Errorf("checkDueAt(%s) = %s, %v; want %s", in, got.Format("2006-01-02 15:04"), err, want)
		}
	}
}

// A closed date is not a school day: a loan that would fall due on one falls
// due on the first day back, and a run of them is skipped whole.
func TestClosedDatesPushTheDueDate(t *testing.T) {
	loc := time.FixedZone("school", 0)
	r := defaultLoanRules()
	r.closed = map[string]bool{"2026-09-29": true, "2026-09-30": true, "2026-10-05": true}
	cases := map[string]string{
		"2026-09-28": "2026-10-01 15:30", // Monday, Tuesday and Wednesday closed -> Thursday
		"2026-10-02": "2026-10-06 15:30", // Friday, Monday closed -> Tuesday
		"2026-10-01": "2026-10-02 15:30", // an open day after -> unchanged
	}
	for day, want := range cases {
		d, _ := time.ParseInLocation("2006-01-02", day, loc)
		if got := r.dueFor(d).Format("2006-01-02 15:04"); got != want {
			t.Errorf("dueFor(%s) = %s, want %s", day, got, want)
		}
	}
	// A due time a client sent for a closed day moves to the day back.
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, loc)
	at := time.Date(2026, 9, 29, 15, 30, 0, 0, loc)
	if got, err := checkDueAt(at, now, r); err != nil || got.Format("2006-01-02 15:04") != "2026-10-01 15:30" {
		t.Errorf("checkDueAt(closed Tuesday) = %s, %v; want Thursday", got, err)
	}
	// The cap moves with the holiday: last day of use next Monday is closed,
	// so its due time is the Tuesday after.
	if got := r.latestDueAt(now).Format("2006-01-02 15:04"); got != "2026-10-06 15:30" {
		t.Errorf("latestDueAt = %s, want 2026-10-06 15:30", got)
	}
}

// A list that closes everything can't hang a checkout.
func TestClosedDatesAreBounded(t *testing.T) {
	r := defaultLoanRules()
	r.closed = map[string]bool{}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 400 {
		r.closed[start.AddDate(0, 0, i).Format(time.DateOnly)] = true
	}
	if got := r.dueFor(start); got.IsZero() {
		t.Fatal("want some due time, got zero")
	}
}

// A shorter cap from settings refuses what the default allows.
func TestMaxCheckoutDaysFromTheRules(t *testing.T) {
	loc := time.FixedZone("school", 0)
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, loc) // a Monday
	r := defaultLoanRules()
	r.maxDays = 2
	if _, err := checkDueAt(r.dueFor(now.AddDate(0, 0, 2)), now, r); err != nil {
		t.Errorf("two days out with a cap of 2 = %v, want ok", err)
	}
	if _, err := checkDueAt(r.dueFor(now.AddDate(0, 0, 3)), now, r); !errors.Is(err, ErrInvalid) {
		t.Errorf("three days out with a cap of 2 = %v, want ErrInvalid", err)
	}
}
