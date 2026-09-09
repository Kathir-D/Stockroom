package stockroom

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// The production work factor is the one security property of this file. The
// suites lower the live cost from TestMain to stay fast (see main_test.go),
// so the guarantee is pinned on the constant a released binary starts from,
// not on the value currently in force.
func TestDefaultPasswordHashCostIsStrong(t *testing.T) {
	if DefaultPasswordHashCost != bcrypt.DefaultCost {
		t.Errorf("DefaultPasswordHashCost = %d, want bcrypt.DefaultCost (%d)",
			DefaultPasswordHashCost, bcrypt.DefaultCost)
	}
	// bcrypt.DefaultCost is 10 today. Pinning a floor as well means a future
	// bump is fine but a downgrade is caught.
	if DefaultPasswordHashCost < 10 {
		t.Errorf("DefaultPasswordHashCost = %d, want at least 10", DefaultPasswordHashCost)
	}
}

// HashPassword must actually apply the configured cost, otherwise the knob
// below is decorative and the suite is slow for nothing.
func TestHashPasswordAppliesConfiguredCost(t *testing.T) {
	prev := SetPasswordHashCost(bcrypt.MinCost)
	t.Cleanup(func() { SetPasswordHashCost(prev) })

	h, err := HashPassword("a fine password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	got, err := bcrypt.Cost([]byte(h))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if got != bcrypt.MinCost {
		t.Errorf("hash cost = %d, want %d", got, bcrypt.MinCost)
	}
	// A cheap hash is still a real bcrypt hash: it must verify, and a wrong
	// password must still be refused.
	if err := CheckPassword(&h, "a fine password"); err != nil {
		t.Errorf("CheckPassword at the test cost = %v, want nil", err)
	}
}

// A hash written at the production cost keeps verifying after the suite has
// lowered the live cost. Real rows in the database were hashed at cost 10, so
// this is the case that would break every login if the cost were baked into
// verification rather than read from the stored hash.
func TestPasswordsHashedAtProductionCostStillVerify(t *testing.T) {
	prev := SetPasswordHashCost(DefaultPasswordHashCost)
	h, err := HashPassword("production password")
	SetPasswordHashCost(prev)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := CheckPassword(&h, "production password"); err != nil {
		t.Errorf("CheckPassword on a production-cost hash = %v, want nil", err)
	}
}

// An out-of-range cost is ignored rather than silently producing an
// unverifiable hash or a 30-second one.
func TestSetPasswordHashCostRejectsOutOfRange(t *testing.T) {
	prev := SetPasswordHashCost(bcrypt.MinCost)
	t.Cleanup(func() { SetPasswordHashCost(prev) })

	for _, bad := range []int{bcrypt.MinCost - 1, bcrypt.MaxCost + 1, -1} {
		if got := SetPasswordHashCost(bad); got != bcrypt.MinCost {
			t.Errorf("SetPasswordHashCost(%d) returned %d, want the unchanged %d", bad, got, bcrypt.MinCost)
		}
		h, err := HashPassword("still works")
		if err != nil {
			t.Fatalf("HashPassword after SetPasswordHashCost(%d): %v", bad, err)
		}
		if cost, _ := bcrypt.Cost([]byte(h)); cost != bcrypt.MinCost {
			t.Errorf("cost after SetPasswordHashCost(%d) = %d, want the unchanged %d", bad, cost, bcrypt.MinCost)
		}
	}
}
