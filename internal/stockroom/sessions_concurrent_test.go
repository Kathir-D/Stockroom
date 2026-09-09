package stockroom

import (
	"sync"
	"testing"
	"time"
)

// The session store is the one piece of mutable state shared by every request
// on the server: two frontends plus the scanner all resolve tokens through the
// same map. These tests exist to be run under -race (CI does, see CI.md); the
// assertions alone would pass on an unsynchronised map, but the detector would
// not.
//
// They use the real clock with an idle timeout long enough that nothing can
// expire mid-test. The fake clock in sessions_test.go is a plain variable, so
// driving it from several goroutines would be a race in the test itself.

const concurrentIdle = time.Hour

// Tokens handed out concurrently must all be distinct and all resolvable. A
// map write lost to a torn update would show up here as a missing session
// rather than as a crash.
func TestSessionStoreConcurrentCreate(t *testing.T) {
	const goroutines, perGoroutine = 16, 50

	s := NewSessionStore(concurrentIdle)
	tokens := make(chan string, goroutines*perGoroutine)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				sess, err := s.Create("profile", false)
				if err != nil {
					t.Errorf("Create: %v", err)
					return
				}
				tokens <- sess.Token
			}
		}()
	}
	wg.Wait()
	close(tokens)

	seen := map[string]bool{}
	for tok := range tokens {
		if seen[tok] {
			t.Fatalf("duplicate token %q handed out to two goroutines", tok)
		}
		seen[tok] = true
	}
	if len(seen) != goroutines*perGoroutine {
		t.Errorf("collected %d tokens, want %d", len(seen), goroutines*perGoroutine)
	}
	if got := s.Len(); got != goroutines*perGoroutine {
		t.Errorf("Len = %d, want %d; a concurrent write was lost", got, goroutines*perGoroutine)
	}
	for tok := range seen {
		if _, ok := s.Get(tok); !ok {
			t.Fatalf("token %q does not resolve after concurrent creation", tok)
		}
	}
}

// Every exported method run against one store at once. The point is the race
// detector; the assertion is that an untouched session survives the storm, so
// a lock held too broadly (or not at all) that dropped entries would fail.
func TestSessionStoreConcurrentMixedOperations(t *testing.T) {
	s := NewSessionStore(concurrentIdle)

	// A session nobody in the loop below refers to. Its profile id is not
	// "churn", so DeleteForProfile must never reach it.
	keep, err := s.Create("bystander", false)
	if err != nil {
		t.Fatal(err)
	}

	const workers, iterations = 8, 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				sess, err := s.Create("churn", i%2 == 0)
				if err != nil {
					t.Errorf("Create: %v", err)
					return
				}
				s.Get(sess.Token)
				s.Upgrade(sess.Token)
				s.Get(keep.Token)
				s.Len()
				switch i % 3 {
				case 0:
					s.Delete(sess.Token)
				case 1:
					s.DeleteForProfile("churn")
				}
			}
		}()
	}
	wg.Wait()

	if _, ok := s.Get(keep.Token); !ok {
		t.Error("the bystander session was dropped by concurrent activity on another profile")
	}
}

// Upgrade races SetInitialPassword against the request that reads the session.
// Whichever order they land in, the flag may only ever move limited -> full:
// an upgraded session must never be observed as limited again, because that
// would send a user who has just set a password back to the set-password
// screen.
func TestSessionStoreUpgradeIsOneWayUnderConcurrency(t *testing.T) {
	s := NewSessionStore(concurrentIdle)
	sess, err := s.Create("p", true)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	upgraded := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Upgrade(sess.Token)
		close(upgraded)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-upgraded
		// Every read after the upgrade has happened must see a full session.
		for i := 0; i < 200; i++ {
			got, ok := s.Get(sess.Token)
			if !ok {
				t.Error("session vanished")
				return
			}
			if got.Limited {
				t.Error("session read as limited after Upgrade returned")
				return
			}
		}
	}()
	wg.Wait()
}

// Get returns a copy. A caller mutating what it got back must not be able to
// reach into the store, or one request could flip another's Limited flag.
func TestSessionGetReturnsCopy(t *testing.T) {
	s := NewSessionStore(concurrentIdle)
	sess, err := s.Create("p", true)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(sess.Token)
	got.Limited = false
	got.ProfileID = "someone-else"

	again, ok := s.Get(sess.Token)
	if !ok {
		t.Fatal("session missing")
	}
	if !again.Limited {
		t.Error("mutating a returned Session cleared Limited in the store")
	}
	if again.ProfileID != "p" {
		t.Errorf("ProfileID = %q, want %q; a returned copy reached the store", again.ProfileID, "p")
	}
}
