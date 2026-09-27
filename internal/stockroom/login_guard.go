package stockroom

import (
	"fmt"
	"sync"
	"time"
)

// The typed-password lockout (CLAUDE.md §7). bcrypt makes each guess cost a
// few tens of milliseconds, which is nothing against a loop on the closet PC,
// so a number that fails too often is refused outright for a while. The
// HTTP layer separately caps how fast the sign-in routes answer at all; this
// is the per-account half, which has to live here so it holds for every
// caller of LoginByPassword.

const (
	// loginFailLimit wrong passwords inside loginFailWindow lock the number.
	loginFailLimit  = 5
	loginFailWindow = 15 * time.Minute
	// loginLockout is how long the lock lasts. Long enough that guessing is
	// hopeless, short enough that a student who fumbled their password five
	// times can try again before the lesson ends. A card scan still works.
	loginLockout = 5 * time.Minute
)

// loginGuard counts recent failures per student number. In memory, like the
// sessions: a restart forgets the counts, and so does everything else.
//
// An attempt is reserved by check and settled by fail, succeed or release.
// Reserving counts it before bcrypt runs, so thirty guesses sent at once are
// not all let through while none of them has failed yet: the attempts still
// in flight count toward the limit alongside the failures already recorded.
type loginGuard struct {
	mu      sync.Mutex
	now     func() time.Time
	fails   map[string][]time.Time
	until   map[string]time.Time
	pending map[string]int
}

// logins is the DB's guard, built on first use so a DB literal in a test
// needs no setup.
func (db *DB) logins() *loginGuard {
	db.loginGuardOnce.Do(func() { db.loginGuard = newLoginGuard() })
	return db.loginGuard
}

func newLoginGuard() *loginGuard {
	return &loginGuard{now: time.Now, fails: map[string][]time.Time{}, until: map[string]time.Time{},
		pending: map[string]int{}}
}

// check refuses a number that is locked, naming how long is left, and
// otherwise reserves an attempt the caller must settle with fail, succeed or
// release.
func (g *loginGuard) check(sn string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if until, ok := g.until[sn]; ok {
		if now.Before(until) {
			return lockedError(until.Sub(now))
		}
		delete(g.until, sn)
	}
	g.pruneLocked(now)
	if len(g.fails[sn])+g.pending[sn] >= loginFailLimit {
		// Enough guesses are already in flight to lock the number if they
		// all fail. Refused rather than queued; the next try after they
		// settle gets an answer.
		return lockedError(loginLockout)
	}
	g.pending[sn]++
	return nil
}

func lockedError(left time.Duration) error {
	mins := int(left.Minutes()) + 1
	return fmt.Errorf("%w: too many wrong passwords for this number. Try again in %s",
		ErrTooManyAttempts, plural(mins, "minute", "minutes"))
}

// release settles a reserved attempt that neither failed nor succeeded, such
// as one the database could not answer.
func (g *loginGuard) release(sn string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.settleLocked(sn)
}

// settleLocked ends one reserved attempt. Caller holds mu.
func (g *loginGuard) settleLocked(sn string) {
	if g.pending[sn] <= 1 {
		delete(g.pending, sn)
	} else {
		g.pending[sn]--
	}
}

// fail records a wrong password and reports whether it locked the number.
func (g *loginGuard) fail(sn string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.settleLocked(sn)
	now := g.now()
	g.pruneLocked(now)
	recent := append(g.fails[sn], now)
	if len(recent) >= loginFailLimit {
		delete(g.fails, sn)
		g.until[sn] = now.Add(loginLockout)
		return true
	}
	g.fails[sn] = recent
	return false
}

// succeed clears a number's count after a correct password.
func (g *loginGuard) succeed(sn string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.settleLocked(sn)
	delete(g.fails, sn)
	delete(g.until, sn)
}

// pruneLocked drops failures older than the window, so the map holds only
// numbers somebody got wrong recently. Caller holds mu.
func (g *loginGuard) pruneLocked(now time.Time) {
	for sn, times := range g.fails {
		kept := times[:0]
		for _, t := range times {
			if now.Sub(t) < loginFailWindow {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(g.fails, sn)
		} else {
			g.fails[sn] = kept
		}
	}
	for sn, until := range g.until {
		if !now.Before(until) {
			delete(g.until, sn)
		}
	}
}
