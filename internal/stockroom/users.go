package stockroom

import (
	"context"
	"fmt"
	"strings"
)

// UserInput is the admin-panel payload for creating or updating an account.
// It never carries a password: an account sets its first one at its first
// scan login (Auth.SetInitialPassword) and an admin replaces it with
// SetUserPassword. Nor a photo: the roster import is the only writer of
// profiles.photo_path (CLAUDE.md §13).
type UserInput struct {
	StudentNumber string  `json:"student_number"`
	FirstName     string  `json:"first_name"`
	LastName      string  `json:"last_name"`
	Email         *string `json:"email"`
	IsAdmin       bool    `json:"is_admin"`
}

// normalize trims every field and validates the student number. Blank
// optional strings become nil so the columns are null rather than "".
func (in *UserInput) normalize() error {
	sn, err := NormalizeStudentNumber(in.StudentNumber)
	if err != nil {
		return err
	}
	in.StudentNumber = sn
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	if in.FirstName == "" && in.LastName == "" {
		return fmt.Errorf("%w: a first or last name is required", ErrInvalid)
	}
	in.Email = trimOptional(in.Email)
	return nil
}

func trimOptional(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// fullName keeps the legacy full_name column in step with the split fields
// so anything still reading it (the old admin screen, the CSV backup) sees a
// sensible value.
func fullName(first, last string) string {
	return strings.TrimSpace(first + " " + last)
}

// ListUsers returns every account, admins first, then by name. Admin only.
func (db *DB) ListUsers(ctx context.Context, actor Actor) ([]Profile, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := db.Pool.Query(ctx,
		`select `+profileColumns+` from profiles
		 order by is_admin desc, last_name nulls last, first_name nulls last, student_number nulls last`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

// GetUser returns one account by id. Admin only; a user reads their own
// profile through Auth.Me.
func (db *DB) GetUser(ctx context.Context, actor Actor, id string) (Profile, error) {
	if err := RequireAdmin(actor); err != nil {
		return Profile{}, err
	}
	p, err := db.profileByID(ctx, id)
	if err != nil {
		return Profile{}, mapPgError("get user", err)
	}
	return p, nil
}

// CreateUser adds an account. A duplicate student number or email is
// ErrConflict. The account starts with no password and sets one at its
// first scan login, the same as a roster import.
func (db *DB) CreateUser(ctx context.Context, actor Actor, in UserInput) (Profile, error) {
	if err := RequireAdmin(actor); err != nil {
		return Profile{}, err
	}
	if err := in.normalize(); err != nil {
		return Profile{}, err
	}
	row := db.Pool.QueryRow(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name, email, is_admin)
		values ($1, $2, $3, $4, $5, $6)
		returning `+profileColumns,
		in.StudentNumber, in.FirstName, in.LastName, fullName(in.FirstName, in.LastName),
		in.Email, in.IsAdmin)
	p, err := scanProfile(row)
	if err != nil {
		return Profile{}, mapPgError("create user", err)
	}
	return p, nil
}

// UpdateUser replaces the editable fields of an account. The password is
// never touched here (use SetUserPassword). An admin cannot remove their own
// admin flag, so the last admin can't lock everyone out of the panel.
func (db *DB) UpdateUser(ctx context.Context, actor Actor, id string, in UserInput) (Profile, error) {
	if err := RequireAdmin(actor); err != nil {
		return Profile{}, err
	}
	if err := in.normalize(); err != nil {
		return Profile{}, err
	}
	if id == actor.ID && !in.IsAdmin {
		return Profile{}, fmt.Errorf("%w: cannot remove your own admin access", ErrConflict)
	}
	row := db.Pool.QueryRow(ctx, `
		update profiles
		set student_number = $2, first_name = $3, last_name = $4, full_name = $5,
		    email = $6, is_admin = $7
		where id = $1
		returning `+profileColumns,
		id, in.StudentNumber, in.FirstName, in.LastName, fullName(in.FirstName, in.LastName),
		in.Email, in.IsAdmin)
	p, err := scanProfile(row)
	if err != nil {
		return Profile{}, mapPgError("update user", err)
	}
	return p, nil
}

// DeleteUser removes an account. It is refused (ErrConflict) while the user
// has anything checked out, and Postgres refuses it for any user with
// custody history at all because custody_events keeps its custodian
// reference; that also comes back as ErrConflict. Deleting yourself is
// refused too. A successful delete drops the user's sessions, so a signed-in
// window dies with the account.
func (db *DB) DeleteUser(ctx context.Context, actor Actor, id string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}
	if id == actor.ID {
		return fmt.Errorf("%w: cannot delete your own account", ErrConflict)
	}
	var open bool
	err := db.Pool.QueryRow(ctx,
		`select exists (select 1 from active_custody where custodian_id = $1)`, id).Scan(&open)
	if err != nil {
		return mapPgError("delete user", err)
	}
	if open {
		return fmt.Errorf("%w: user has items checked out", ErrConflict)
	}
	tag, err := db.Pool.Exec(ctx, `delete from profiles where id = $1`, id)
	if err != nil {
		return mapPgError("delete user", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	db.Sessions.DeleteForProfile(id)
	return nil
}

// SetUserPassword is the admin reset: it replaces the hash whether or not
// one exists, and drops the user's sessions so a session opened with the old
// password cannot outlive it.
func (db *DB) SetUserPassword(ctx context.Context, actor Actor, id, password string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := db.Pool.Exec(ctx, `update profiles set password_hash = $2 where id = $1`, id, hash)
	if err != nil {
		return mapPgError("set user password", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	db.Sessions.DeleteForProfile(id)
	return nil
}
