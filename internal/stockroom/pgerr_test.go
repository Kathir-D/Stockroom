package stockroom

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// mapPgError decides which Postgres failures are the caller's fault (4xx) and
// which are the server's (500). Getting that wrong either hides a real bug
// behind a 409 or answers 500 for a duplicate student number, so each SQLSTATE
// is pinned here rather than only through the handful of paths that happen to
// provoke one.
func TestMapPgError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		want       error   // sentinel the result must wrap, nil for "none"
		wantNot    []error // sentinels it must NOT wrap
		wantSubstr string
	}{
		{
			name:       "unique violation is a conflict",
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "profiles_student_number_key"},
			want:       ErrConflict,
			wantSubstr: "student number already in use",
		},
		{
			name:       "foreign key violation is a conflict",
			err:        &pgconn.PgError{Code: "23503", ConstraintName: "custody_events_custodian_id_fkey"},
			want:       ErrConflict,
			wantSubstr: "user has custody history",
		},
		{
			name:       "malformed uuid or enum is invalid input",
			err:        &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "not-a-uuid"`},
			want:       ErrInvalid,
			wantSubstr: "invalid input syntax",
		},
		{
			// A constraint the mapping does not name still has to be a
			// conflict; only the wording falls back.
			name:       "unknown constraint still maps by code",
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "tags_name_key"},
			want:       ErrConflict,
			wantSubstr: "tags_name_key",
		},
		{
			// Anything else is a server problem: it must not become a 4xx.
			name:    "an unrecognised code is not a client error",
			err:     &pgconn.PgError{Code: "42601", Message: "syntax error at or near"},
			want:    nil,
			wantNot: []error{ErrConflict, ErrInvalid, ErrNotFound},
		},
		{
			name:    "a plain error passes through wrapped",
			err:     errors.New("connection reset by peer"),
			want:    nil,
			wantNot: []error{ErrConflict, ErrInvalid},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mapPgError("do the thing", c.err)
			if got == nil {
				t.Fatal("mapPgError returned nil for a non-nil error")
			}
			if c.want != nil && !errors.Is(got, c.want) {
				t.Errorf("mapPgError = %v, want it to wrap %v", got, c.want)
			}
			for _, no := range c.wantNot {
				if errors.Is(got, no) {
					t.Errorf("mapPgError = %v, must not wrap %v", got, no)
				}
			}
			if c.wantSubstr != "" && !strings.Contains(got.Error(), c.wantSubstr) {
				t.Errorf("mapPgError = %q, want it to mention %q", got, c.wantSubstr)
			}
			// Unmapped errors keep the operation name so a 500 is traceable
			// in the log.
			if c.want == nil && !strings.Contains(got.Error(), "do the thing") {
				t.Errorf("mapPgError = %q, want the operation name preserved", got)
			}
		})
	}
}
