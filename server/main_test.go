package main

import (
	"io"
	"log"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"stockroom/internal/stockroom"
)

// TestMain silences handler logging (errors are logged on purpose, and the
// suite exercises those paths deliberately) and lowers the bcrypt work factor
// for the same reason internal/stockroom does: the HTTP suite seeds accounts
// with passwords, and the production cost would dominate its runtime. See
// stockroom.SetPasswordHashCost.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	stockroom.SetPasswordHashCost(bcrypt.MinCost)
	m.Run()
}
