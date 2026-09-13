package stockroom

import (
	"testing"
	"time"
)

// fakeClock lets the idle timeout be tested without sleeping.
func newTestStore(idle time.Duration) (*SessionStore, *time.Time) {
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	s := NewSessionStore(idle)
	s.now = func() time.Time { return now }
	return s, &now
}

func TestSessionCreateAndGet(t *testing.T) {
	s, _ := newTestStore(30 * time.Minute)
	sess, err := s.Create("profile-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Token) != 64 {
		t.Errorf("token = %q, want 64 hex characters", sess.Token)
	}
	got, ok := s.Get(sess.Token)
	if !ok || got.ProfileID != "profile-1" || got.Limited {
		t.Errorf("Get = %+v, %v; want the created session", got, ok)
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("an unknown token resolved")
	}
	if _, ok := s.Get(""); ok {
		t.Error("an empty token resolved")
	}
}
