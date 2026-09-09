package stockroom

import (
	"context"
	"fmt"
	"strings"
)

// EnsureFailsafeAdmin upserts the account named by ADMIN_STUDENT_NUMBER with
// is_admin = true and the bcrypt hash of ADMIN_PASSWORD. The server calls it
// on every start (CLAUDE.md §7) so there is always a way into the admin
// panel that no UI or roster import can lock out. If the account already
// exists, its name and photo are left alone; only the admin flag and the
// password are forced.
//
// Either value being blank means the operator has not configured a failsafe
// admin. That is allowed: the call writes nothing and returns
// ErrFailsafeNotConfigured. Deciding what "not configured" means belongs here
// rather than in the caller, so the rule lives in one place and is covered by
// failsafe_test.go.
func (db *DB) EnsureFailsafeAdmin(ctx context.Context, studentNumber, password string) error {
	if strings.TrimSpace(studentNumber) == "" || password == "" {
		return ErrFailsafeNotConfigured
	}

	sn, err := NormalizeStudentNumber(studentNumber)
	if err != nil {
		return fmt.Errorf("ADMIN_STUDENT_NUMBER: %w", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("ADMIN_PASSWORD: %w", err)
	}

	_, err = db.Pool.Exec(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name, is_admin, password_hash)
		values ($1, 'Failsafe', 'Admin', 'Failsafe Admin', true, $2)
		on conflict (student_number) do update
		set is_admin = true,
		    password_hash = excluded.password_hash`,
		sn, hash)
	if err != nil {
		return fmt.Errorf("ensure failsafe admin: %w", err)
	}
	return nil
}
