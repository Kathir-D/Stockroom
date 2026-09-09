package stockroom

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Session is one signed-in browser or window. Sessions live only in memory
// (CLAUDE.md §7): restarting the server signs everyone out, which is fine for
// a single shared closet PC.
type Session struct {
	Token     string
	ProfileID string
	// Limited marks a scan login by an account with no password yet. The
	// only call such a session may make is SetInitialPassword; everything
	// else is refused until the password is set and the session upgraded.
	Limited  bool
	LastSeen time.Time
}

// SessionStore is the in-memory session map with an idle timeout. A session
// whose last request is older than the timeout is gone on its next use.
type SessionStore struct {
	idle time.Duration
	now  func() time.Time // injectable for tests

	mu       sync.Mutex
	sessions map[string]*Session
}

// NewSessionStore returns an empty store. idle is SESSION_IDLE_MINUTES as a
// duration; a zero or negative value disables expiry, which no caller wants
// in production but keeps the store usable in tests.
func NewSessionStore(idle time.Duration) *SessionStore {
	return &SessionStore{
		idle:     idle,
		now:      time.Now,
		sessions: map[string]*Session{},
	}
}

// Create opens a session for profileID and returns it. Expired sessions are
// swept on every create so the map stays bounded without a background
// goroutine.
func (s *SessionStore) Create(profileID string, limited bool) (Session, error) {
	token, err := newToken()
	if err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	sess := &Session{Token: token, ProfileID: profileID, Limited: limited, LastSeen: s.now()}
	s.sessions[token] = sess
	return *sess, nil
}

// Get returns the session for token and stamps its last-seen time. A missing
// or idle-expired token returns ok = false; expired ones are removed as a
// side effect.
func (s *SessionStore) Get(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return Session{}, false
	}
	now := s.now()
	if s.expired(sess, now) {
		delete(s.sessions, token)
		return Session{}, false
	}
	sess.LastSeen = now
	return *sess, true
}

// Upgrade clears the Limited flag after the account sets its first password.
// Unknown tokens are ignored.
func (s *SessionStore) Upgrade(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[token]; ok {
		sess.Limited = false
	}
}

// Delete ends one session. Deleting an unknown token is a no-op.
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// DeleteForProfile ends every session belonging to profileID, used when an
// admin deletes the account or resets its password.
func (s *SessionStore) DeleteForProfile(profileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, sess := range s.sessions {
		if sess.ProfileID == profileID {
			delete(s.sessions, token)
		}
	}
}

// Len reports the number of live (unexpired) sessions.
func (s *SessionStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	return len(s.sessions)
}

func (s *SessionStore) expired(sess *Session, now time.Time) bool {
	return s.idle > 0 && now.Sub(sess.LastSeen) > s.idle
}

func (s *SessionStore) sweepLocked() {
	now := s.now()
	for token, sess := range s.sessions {
		if s.expired(sess, now) {
			delete(s.sessions, token)
		}
	}
}

// newToken returns 32 random bytes as hex. Tokens are only ever compared by
// map lookup, so they need to be unguessable, nothing more.
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
