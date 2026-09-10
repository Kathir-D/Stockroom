package stockroom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
			err:        &pgconn.PgError{Code: "23505", ConstraintName: "assets_asset_tag_key"},
			want:       ErrConflict,
			wantSubstr: "assets_asset_tag_key",
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

func TestMapPgErrorNilIsNil(t *testing.T) {
	if err := mapPgError("op", nil); err != nil {
		t.Errorf("mapPgError(nil) = %v, want nil", err)
	}
}

// constraintMessage is what the admin panel shows. Anything without a
// hand-written phrase falls back to the constraint name, and failing that to
// the driver's message, so the UI never renders an empty string.
func TestConstraintMessageFallbacks(t *testing.T) {
	cases := []struct {
		name string
		err  *pgconn.PgError
		want string
	}{
		{"student number", &pgconn.PgError{ConstraintName: "profiles_student_number_key"}, "student number already in use"},
		{"email", &pgconn.PgError{ConstraintName: "profiles_email_key"}, "email already in use"},
		{"custodian fk", &pgconn.PgError{ConstraintName: "custody_events_custodian_id_fkey"}, "user has custody history"},
		{"checked out by fk", &pgconn.PgError{ConstraintName: "custody_events_checked_out_by_fkey"}, "user has custody history"},
		{"checked in by fk", &pgconn.PgError{ConstraintName: "custody_events_checked_in_by_fkey"}, "user has custody history"},
		{"unnamed constraint falls back to the message", &pgconn.PgError{Message: "some database detail"}, "some database detail"},
		{"unknown constraint falls back to its name", &pgconn.PgError{ConstraintName: "kits_name_key", Message: "ignored"}, "kits_name_key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := constraintMessage(c.err); got != c.want {
				t.Errorf("constraintMessage = %q, want %q", got, c.want)
			}
		})
	}
}

// The constraint names above are hardcoded strings matched against whatever
// Postgres actually reports. A migration that renames one would silently
// downgrade the message to a raw identifier, so provoke each violation for
// real and assert the phrase the admin panel would show.
func TestConstraintMessagesMatchTheLiveSchema(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)

	t.Run("duplicate student number", func(t *testing.T) {
		existing := insertTestProfile(t, db, false, "")
		_, err := db.CreateUser(ctx, admin, UserInput{
			StudentNumber: *existing.StudentNumber,
			FirstName:     "Duplicate",
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("CreateUser with a taken student number = %v, want ErrConflict", err)
		}
		if !strings.Contains(err.Error(), "student number already in use") {
			t.Errorf("error = %q, want the plain-words message; did profiles_student_number_key get renamed?", err)
		}
	})

	t.Run("duplicate email", func(t *testing.T) {
		email := fmt.Sprintf("dup-%d@example.test", time.Now().UnixNano())
		first := insertTestProfile(t, db, false, "")
		if _, err := db.Pool.Exec(ctx, `update profiles set email = $2 where id = $1`, first.ID, email); err != nil {
			t.Fatal(err)
		}
		_, err := db.CreateUser(ctx, admin, UserInput{
			StudentNumber: testStudentNumber(t, db),
			FirstName:     "Same",
			Email:         &email,
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("CreateUser with a taken email = %v, want ErrConflict", err)
		}
		if !strings.Contains(err.Error(), "email already in use") {
			t.Errorf("error = %q, want the plain-words message; did profiles_email_key get renamed?", err)
		}
	})

	// A user who has ever held an item keeps a custody_events row referencing
	// them, so Postgres refuses the delete. That has to read as a conflict the
	// admin can understand, not a 500.
	t.Run("user with custody history", func(t *testing.T) {
		p := insertTestProfile(t, db, false, "")
		// The asset is created by the admin, not by p: assets.created_by also
		// references profiles, and it would block the delete first with a
		// different constraint. This case is specifically about the custody
		// trail being what holds the account.
		asset := insertTestAsset(t, db, adminP)
		openCustody(t, db, asset, p, p, time.Now().Add(24*time.Hour))
		// Close it out, so the block is the history, not an open loan.
		if _, err := db.Pool.Exec(ctx,
			`update custody_events set checked_in_at = now() where asset_id = $1`, asset); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `update assets set status = 'available' where id = $1`, asset); err != nil {
			t.Fatal(err)
		}

		err := db.DeleteUser(ctx, admin, p.ID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("DeleteUser with custody history = %v, want ErrConflict", err)
		}
		if !strings.Contains(err.Error(), "user has custody history") {
			t.Errorf("error = %q, want the plain-words message; did a custody_events FK get renamed?", err)
		}
	})

	// assets.created_by also references profiles, so a user who ever added an
	// asset is held by that key instead. constraintMessage has no phrase for
	// it and falls back to the raw identifier, which is ugly but still a 409
	// the admin panel can act on rather than a 500. The status is the part
	// worth pinning; if the wording is improved later, only the second
	// assertion needs updating.
	t.Run("user who created an asset", func(t *testing.T) {
		p := insertTestProfile(t, db, false, "")
		insertTestAsset(t, db, p)

		err := db.DeleteUser(ctx, admin, p.ID)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("DeleteUser for an asset creator = %v, want ErrConflict, not a 500", err)
		}
		if !strings.Contains(err.Error(), "assets_created_by_fkey") {
			t.Logf("note: the message is now %q; constraintMessage may have gained a phrase for this key", err)
		}
	})
}
