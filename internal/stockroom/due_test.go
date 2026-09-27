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
		got, err := DueFor(d, "15:30")
		if err != nil {
			t.Fatal(err)
		}
		if got.Format("2006-01-02 15:04") != want {
			t.Errorf("DueFor(%s) = %s, want %s", day, got.Format("2006-01-02 15:04"), want)
		}
	}
	if _, err := DueFor(time.Now(), "3:30pm"); !errors.Is(err, ErrInvalid) {
		t.Errorf("DueFor with a bad time = %v, want ErrInvalid", err)
	}
}

// The cap is on the last day of use: seven days out is allowed, with its due
// time on the school day after, and anything later is not.
func TestCheckDueAtCapsTheLastDayOfUse(t *testing.T) {
	loc := time.FixedZone("school", 0)
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, loc) // a Monday
	lastDay := now.AddDate(0, 0, MaxCheckoutDays)   // next Monday
	due, _ := DueFor(lastDay, "15:30")              // next Tuesday 15:30
	if err := checkDueAt(due, now, "15:30"); err != nil {
		t.Errorf("due after the seventh day = %v, want nil", err)
	}
	if err := checkDueAt(due.Add(time.Minute), now, "15:30"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a minute past the cap = %v, want ErrInvalid", err)
	}
	if err := checkDueAt(now.Add(-time.Minute), now, "15:30"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a due time in the past = %v, want ErrInvalid", err)
	}
	// Picking today means due the next school day, never already overdue.
	today, _ := DueFor(now, "15:30")
	if err := checkDueAt(today, now, "15:30"); err != nil || !today.After(now.Add(24*time.Hour)) {
		t.Errorf("due for today's last day = %v (%v), want tomorrow afternoon", today, err)
	}
}
