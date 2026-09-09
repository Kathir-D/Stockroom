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

func TestSessionTokensAreUnique(t *testing.T) {
	s, _ := newTestStore(0)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		sess, err := s.Create("p", false)
		if err != nil {
			t.Fatal(err)
		}
		if seen[sess.Token] {
			t.Fatal("duplicate token")
		}
		seen[sess.Token] = true
	}
}

// A session expires after idle time with no requests, and every request
// pushes the deadline out.
func TestSessionIdleTimeout(t *testing.T) {
	s, now := newTestStore(30 * time.Minute)
	sess, _ := s.Create("p", false)

	*now = now.Add(20 * time.Minute)
	if _, ok := s.Get(sess.Token); !ok {
		t.Fatal("session expired before the idle timeout")
	}
	// The Get above touched it, so another 20 minutes is still inside.
	*now = now.Add(20 * time.Minute)
	if _, ok := s.Get(sess.Token); !ok {
		t.Fatal("an active session expired")
	}
	*now = now.Add(31 * time.Minute)
	if _, ok := s.Get(sess.Token); ok {
		t.Fatal("session survived past the idle timeout")
	}
	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0 after expiry", s.Len())
	}
}

func TestSessionZeroIdleNeverExpires(t *testing.T) {
	s, now := newTestStore(0)
	sess, _ := s.Create("p", false)
	*now = now.Add(365 * 24 * time.Hour)
	if _, ok := s.Get(sess.Token); !ok {
		t.Error("session with no idle timeout expired")
	}
}

func TestSessionUpgradeAndDelete(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	sess, _ := s.Create("p", true)
	if got, _ := s.Get(sess.Token); !got.Limited {
		t.Fatal("session should start limited")
	}
	s.Upgrade(sess.Token)
	if got, _ := s.Get(sess.Token); got.Limited {
		t.Error("Upgrade did not clear Limited")
	}
	s.Upgrade("unknown") // no panic
	s.Delete(sess.Token)
	if _, ok := s.Get(sess.Token); ok {
		t.Error("deleted session still resolves")
	}
	s.Delete(sess.Token) // idempotent
}

func TestSessionDeleteForProfile(t *testing.T) {
	s, _ := newTestStore(time.Hour)
	a1, _ := s.Create("alice", false)
	a2, _ := s.Create("alice", false)
	b, _ := s.Create("bob", false)
	s.DeleteForProfile("alice")
	if _, ok := s.Get(a1.Token); ok {
		t.Error("alice's first session survived")
	}
	if _, ok := s.Get(a2.Token); ok {
		t.Error("alice's second session survived")
	}
	if _, ok := s.Get(b.Token); !ok {
		t.Error("bob's session was dropped")
	}
}

// Expired sessions are swept on Create so the map cannot grow without bound
// on a machine that never restarts.
func TestSessionSweepOnCreate(t *testing.T) {
	s, now := newTestStore(time.Minute)
	for i := 0; i < 5; i++ {
		s.Create("p", false)
	}
	*now = now.Add(2 * time.Minute)
	s.Create("p", false)
	if n := len(s.sessions); n != 1 {
		t.Errorf("map holds %d sessions, want 1 after sweep", n)
	}
}
