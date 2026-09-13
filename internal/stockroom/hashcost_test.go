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
