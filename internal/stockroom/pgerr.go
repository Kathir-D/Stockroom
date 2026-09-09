package stockroom

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// mapPgError turns the Postgres errors that mean "bad request" into the
// package sentinels so server/ can answer with 4xx instead of 500:
// unique_violation and foreign_key_violation are conflicts (a duplicate
// student number, a user with custody history), invalid_text_representation
// is a malformed id or enum value from the client. Everything else passes
// through wrapped with op, the name of the operation that failed.
func mapPgError(op string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", ErrConflict, constraintMessage(pgErr))
		case "23503": // foreign_key_violation
			return fmt.Errorf("%w: %s", ErrConflict, constraintMessage(pgErr))
		case "22P02": // invalid_text_representation (bad uuid, bad enum)
			return fmt.Errorf("%w: %s", ErrInvalid, pgErr.Message)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// constraintMessage names the constraint in plain words where one is known,
// so the frontend can show "student number already in use" without parsing
// Postgres output.
func constraintMessage(e *pgconn.PgError) string {
	switch e.ConstraintName {
	case "profiles_student_number_key":
		return "student number already in use"
	case "profiles_email_key":
		return "email already in use"
	case "custody_events_custodian_id_fkey", "custody_events_checked_out_by_fkey", "custody_events_checked_in_by_fkey":
		return "user has custody history"
	}
	if e.ConstraintName != "" {
		return e.ConstraintName
	}
	return e.Message
}
