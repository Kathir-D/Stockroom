package stockroom

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain lowers the bcrypt work factor for the whole package. The suite
// creates several hundred accounts, and at the production cost every one of
// them costs tens of milliseconds of deliberate key stretching, which is most
// of the suite's runtime and far worse under -race.
//
// Nothing here weakens what ships: DefaultPasswordHashCost is what a released
// binary uses and hashcost_test.go pins it. Verification reads the cost out of
// each stored hash, so cheap fixtures and production rows both verify.
func TestMain(m *testing.M) {
	SetPasswordHashCost(bcrypt.MinCost)
	m.Run()
}
