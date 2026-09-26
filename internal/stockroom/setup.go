package stockroom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The first-run wizard (CLAUDE.md §13, Phase B).
//
// Two halves with different trust, and the split is the design:
//
//   - **Creating the first admin** is the one thing the wizard does with no
//     session, because there is nobody to have one. It exists only while the
//     profiles table is empty, and that check and the insert happen under a
//     table lock in one transaction, so two browsers racing the form produce
//     one admin and one refusal rather than two admins.
//
//   - **Everything after that** is an ordinary admin-only call. The wizard is a
//     guided route through things the admin panel already does -- imports,
//     settings, a backup -- plus the two that only it does: the failsafe, and
//     the example data.
//
// Why not "the wizard exists only while there are no accounts", which is what
// the plan said: install.sh prompts for the failsafe admin, and the server
// creates that account at boot, so on the installs this project actually
// produces the table is never empty by the time a browser opens. A wizard
// gated on an empty table would be unreachable exactly where it is needed.
// So the unauthenticated part keeps that gate, and the rest is gated on
// setup_completed_at instead.

// SetupState is what the sign-in screen and the wizard need to know.
type SetupState struct {
	// NeedsAdmin: there are no accounts at all, so the sign-in screen shows
	// "create the first admin" instead of asking for an ID.
	NeedsAdmin bool `json:"needs_admin"`
	// Step is where the wizard resumes; Completed is whether it was finished
	// or dismissed. Only reported to an admin.
	Step      int  `json:"step"`
	Completed bool `json:"completed"`
	// FailsafeConfigured: a failsafe admin was ensured at start-up (or by the
	// wizard since). FailsafeWritable: the server knows which settings file
	// to write one into.
	FailsafeConfigured bool `json:"failsafe_configured"`
	FailsafeWritable   bool `json:"failsafe_writable"`
	// ExamplesPresent: example items or example people are loaded, so the
	// assets screen can offer to remove them.
	ExamplesPresent bool `json:"examples_present"`
}

// NeedsFirstAdmin reports whether there are no accounts at all. Public: it is
// what decides the sign-in screen's shape, and "this install has nobody in it
// yet" is visible to anyone looking at that screen anyway.
func (db *DB) NeedsFirstAdmin(ctx context.Context) (bool, error) {
	var any bool
	if err := db.Pool.QueryRow(ctx, `select exists (select 1 from profiles)`).Scan(&any); err != nil {
		return false, mapPgError("check accounts", err)
	}
	return !any, nil
}

// GetSetup is the wizard's read.
func (db *DB) GetSetup(ctx context.Context, actor Actor) (SetupState, error) {
	if err := RequireAdmin(actor); err != nil {
		return SetupState{}, err
	}
	var st SetupState
	var completedAt *time.Time
	err := db.Pool.QueryRow(ctx, `
		select setup_step, setup_completed_at,
		       exists (select 1 from assets where `+exampleAssetSQL+`)
		    or exists (select 1 from profiles where `+exampleProfileSQL+`)
		  from app_settings where id = true`).Scan(&st.Step, &completedAt, &st.ExamplesPresent)
	if err != nil {
		return SetupState{}, mapPgError("read setup", err)
	}
	st.Completed = completedAt != nil
	st.FailsafeConfigured = failsafeAdminConfigured()
	st.FailsafeWritable = db.EnvPath != ""
	return st, nil
}

// SaveSetupProgress records the step to resume at, and completion. Finishing
// and "skip the rest" are the same write: either way the admin should stop
// being sent here after sign-in.
func (db *DB) SaveSetupProgress(ctx context.Context, actor Actor, step int, completed bool) (SetupState, error) {
	if err := RequireAdmin(actor); err != nil {
		return SetupState{}, err
	}
	if step < 0 || step > 20 {
		return SetupState{}, fmt.Errorf("%w: setup step %d is out of range", ErrInvalid, step)
	}
	_, err := db.Pool.Exec(ctx, `
		update app_settings
		   set setup_step = $1,
		       setup_completed_at = case when $2 then coalesce(setup_completed_at, now()) else null end
		 where id = true`, step, completed)
	if err != nil {
		return SetupState{}, mapPgError("save setup", err)
	}
	return db.GetSetup(ctx, actor)
}

// FirstAdminInput is the wizard's step 2. The student-number format rides
// along because it has to be decided before the admin's own number can be
// checked against it, and there is no session yet to save it with.
type FirstAdminInput struct {
	StudentNumber        string `json:"student_number"`
	FirstName            string `json:"first_name"`
	LastName             string `json:"last_name"`
	Password             string `json:"password"`
	StudentNumberFormat  string `json:"student_number_format"`
	StudentNumberPattern string `json:"student_number_pattern"`
}

// CreateFirstAdmin makes the first account and signs it in. Refused with
// ErrConflict once any account exists.
func (db *DB) CreateFirstAdmin(ctx context.Context, in FirstAdminInput) (LoginResult, error) {
	format := StudentNumberFormat(strings.TrimSpace(in.StudentNumberFormat))
	if format == "" {
		format = FormatDigits
	}
	pattern := strings.TrimSpace(in.StudentNumberPattern)
	matches, err := studentNumberMatcher(format, pattern)
	if err != nil {
		return LoginResult{}, err
	}
	number := strings.TrimSpace(in.StudentNumber)
	if number == "" || len(number) > MaxStudentNumberLength || !matches(number) {
		return LoginResult{}, fmt.Errorf("%w: that ID number does not fit the format you picked", ErrInvalid)
	}
	first, last := strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName)
	if first == "" {
		return LoginResult{}, fmt.Errorf("%w: your first name is needed", ErrInvalid)
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return LoginResult{}, err
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return LoginResult{}, mapPgError("create first admin", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// SHARE ROW EXCLUSIVE conflicts with itself and with every insert, so a
	// second request waits here and then sees the first one's row.
	if _, err := tx.Exec(ctx, `lock table profiles in share row exclusive mode`); err != nil {
		return LoginResult{}, mapPgError("create first admin", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from profiles)`).Scan(&exists); err != nil {
		return LoginResult{}, mapPgError("create first admin", err)
	}
	if exists {
		return LoginResult{}, fmt.Errorf("%w: Stockroom already has an admin. Sign in instead", ErrConflict)
	}

	if _, err := tx.Exec(ctx, `
		update app_settings
		   set student_number_format = $1, student_number_pattern = $2, updated_at = now()
		 where id = true`, string(format), nullable(pattern)); err != nil {
		return LoginResult{}, mapPgError("create first admin", err)
	}
	full := strings.TrimSpace(first + " " + last)
	p, err := scanProfile(tx.QueryRow(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name, is_admin, password_hash)
		values ($1, $2, $3, $4, true, $5)
		returning `+profileColumns, number, first, nullable(last), full, hash))
	if err != nil {
		return LoginResult{}, mapPgError("create first admin", err)
	}
	if err := writeLog(ctx, tx, LogEntry{
		Category: LogAdmin, Action: "first_admin_created", ActorID: p.ID,
		Summary: fmt.Sprintf("Created the first admin account, %s", profileLabel(p)),
	}); err != nil {
		return LoginResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LoginResult{}, mapPgError("create first admin", err)
	}

	// Validated above, so this cannot fail; the process now agrees with the
	// row it just committed.
	_ = SetStudentNumberFormat(format, pattern)
	return db.openSession(ctx, p, false, "password")
}

// ConfigureFailsafe writes the failsafe admin into the server's .env and
// ensures the account now, rather than at the next restart.
//
// The .env and not the database, because the failsafe's whole job is getting
// back in after the database is lost (CLAUDE.md §7, §11): an account that
// lived only in the database would be restored-from-empty along with
// everything else.
func (db *DB) ConfigureFailsafe(ctx context.Context, actor Actor, number, password string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}
	if db.EnvPath == "" {
		return fmt.Errorf("%w: Stockroom was started without a settings file, so the spare account has nowhere to be kept. Set ADMIN_STUDENT_NUMBER and ADMIN_PASSWORD where Stockroom is started", ErrNotConfigured)
	}
	sn, err := NormalizeStudentNumber(number)
	if err != nil {
		return err
	}
	// Not the admin's own number. The failsafe's password is re-applied on
	// every start, so sharing a number would reset this person's password to
	// the spare one each time the machine rebooted.
	if actor.StudentNumber == sn {
		return fmt.Errorf("%w: use a different number from your own. The spare account's password is put back every time Stockroom starts, so sharing your number would keep resetting your password", ErrInvalid)
	}
	if _, err := HashPassword(password); err != nil {
		return err
	}
	// Refused before anything is written if some other, ordinary account
	// already has that number: EnsureFailsafeAdmin would otherwise quietly
	// promote that person to admin.
	var existing string
	err = db.Pool.QueryRow(ctx, `
		select coalesce(full_name, student_number) from profiles
		 where student_number = $1 and full_name is distinct from 'Failsafe Admin'`, sn).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("%w: %s already has that number. Pick one nobody uses", ErrInvalid, existing)
	case !errors.Is(err, pgx.ErrNoRows):
		return mapPgError("check failsafe number", err)
	}

	if err := setEnvValues(db.EnvPath, map[string]string{
		"ADMIN_STUDENT_NUMBER": sn,
		"ADMIN_PASSWORD":       password,
	}); err != nil {
		return err
	}
	if err := db.EnsureFailsafeAdmin(ctx, sn, password); err != nil {
		return err
	}
	SetFailsafeAdminConfigured(true)

	// Replacing the failsafe has to retire the old one. Nothing re-applies
	// its password any more, but the account itself would stay an admin with
	// that password for good -- and "the old one may have leaked" is the
	// likeliest reason to replace it. Every failsafe-created account other
	// than this one, rather than the number .env held a moment ago: after a
	// failure between the file write and here, a retry would read the new
	// number back and retire nothing. Demoted and password cleared rather
	// than deleted, so any custody history naming it survives.
	rows, err := db.Pool.Query(ctx, `
		update profiles set is_admin = false, password_hash = null
		 where full_name = 'Failsafe Admin' and is_admin and student_number <> $1
		returning id`, sn)
	if err != nil {
		return mapPgError("retire previous failsafe", err)
	}
	retired, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return mapPgError("retire previous failsafe", err)
	}
	for _, id := range retired {
		db.Sessions.DeleteForProfile(id)
	}
	db.logBestEffort(ctx, LogEntry{Category: LogAdmin, Action: "failsafe_configured", ActorID: actorLogID(actor),
		Summary: "Set the failsafe admin account"})
	return nil
}

// Example rows are recognised by content, not by a reserved id. The plan was
// a reserved UUID prefix, but the rows come in through the real importers --
// the same code a school's own CSV goes through, which is what makes the
// examples worth trying -- and those generate their own ids. So each kind is
// matched on two things a real row will not have together: the EXAMPLE-
// serial and a name starting "Example", or a 90000x number and the first name
// "Example". Deleting on one of them alone could take a real student whose
// number happens to be 900003.
const (
	exampleAssetSQL   = `serial_number like 'EXAMPLE-%' and name like 'Example %'`
	exampleProfileSQL = `student_number like '90000_' and first_name = 'Example'`
)

// ExamplesResult says what loading the examples did.
type ExamplesResult struct {
	Categories CategoryImportResult `json:"categories"`
	Assets     AssetImportResult    `json:"assets"`
	People     RosterResult         `json:"people"`
	// Skipped names what was left out and why, in a sentence.
	Skipped []string `json:"skipped,omitempty"`
}

// LoadExamples imports the media-department tree, the example items and the
// example people, through the same importers the admin panel uses. files is
// the examples package's embedded FS.
func (db *DB) LoadExamples(ctx context.Context, actor Actor, files fs.FS) (ExamplesResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return ExamplesResult{}, err
	}
	read := func(name string) (*bytes.Reader, error) {
		b, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("examples: %w", err)
		}
		return bytes.NewReader(b), nil
	}

	var res ExamplesResult
	r, err := read("categories.media-department.md")
	if err != nil {
		return res, err
	}
	if res.Categories, err = db.ImportCategories(ctx, actor, r); err != nil {
		return res, fmt.Errorf("the example category tree: %w", err)
	}
	// Both importers UPSERT, which is right for a school's own file and wrong
	// here: a real student numbered 900004 would be renamed "Example
	// Student-Four", and RemoveExamples would then delete them. So if any
	// real row already holds an example serial or number, that half is
	// skipped and the result says so.
	var realAsset, realPerson bool
	if err := db.Pool.QueryRow(ctx, `
		select exists (select 1 from assets where serial_number like 'EXAMPLE-%' and name not like 'Example %'),
		       exists (select 1 from profiles where student_number like '90000_' and first_name is distinct from 'Example')`).
		Scan(&realAsset, &realPerson); err != nil {
		return res, mapPgError("load examples", err)
	}

	if realAsset {
		res.Skipped = append(res.Skipped, "The example items were not added: one of your own items already uses an EXAMPLE- serial.")
	} else {
		if r, err = read("assets.csv"); err != nil {
			return res, err
		}
		if res.Assets, err = db.ImportAssets(ctx, actor, r); err != nil {
			return res, fmt.Errorf("the example items: %w", err)
		}
	}

	if realPerson {
		res.Skipped = append(res.Skipped, "The example people were not added: somebody real already has a number from 900001 to 900009.")
		return res, nil
	}
	if r, err = read("roster.csv"); err != nil {
		return res, err
	}
	res.People, err = db.ImportRoster(ctx, actor, r, "")
	if errors.Is(err, ErrNotConfigured) {
		// No uploads folder is a development machine, not a failure worth
		// losing the items over.
		res.Skipped = append(res.Skipped, "The example people were not added: "+err.Error())
		err = nil
	}
	if err == nil {
		db.logBestEffort(ctx, LogEntry{Category: LogAdmin, Action: "examples_loaded", ActorID: actorLogID(actor),
			Summary: "Loaded the example data"})
	}
	return res, err
}

// RemoveExamples deletes the example items and people. The category tree is
// left alone: a school that loaded the example tree has most likely started
// editing it into its own, and a tree has no fake data in it.
//
// Items go first, and their custody rows with them (the foreign key
// cascades). An example person is then deleted only if nothing else still
// points at them -- if somebody tried the system by checking a *real* camera
// out to "Example Student-One", that history belongs to the camera and is
// kept, along with the person it names. Kept is reported, not hidden.
func (db *DB) RemoveExamples(ctx context.Context, actor Actor) (ExamplesRemoved, error) {
	if err := RequireAdmin(actor); err != nil {
		return ExamplesRemoved{}, err
	}
	var out ExamplesRemoved
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return out, mapPgError("remove examples", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `delete from assets where `+exampleAssetSQL)
	if err != nil {
		return out, mapPgError("remove examples", err)
	}
	out.Assets = int(tag.RowsAffected())

	rows, err := tx.Query(ctx, `select id from profiles where `+exampleProfileSQL)
	if err != nil {
		return out, mapPgError("remove examples", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, mapPgError("remove examples", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	var deleted []string
	for _, id := range ids {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return out, mapPgError("remove examples", err)
		}
		if _, err := sp.Exec(ctx, `delete from profiles where id = $1`, id); err != nil {
			_ = sp.Rollback(ctx)
			out.PeopleKept++
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			return out, mapPgError("remove examples", err)
		}
		deleted = append(deleted, id)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, mapPgError("remove examples", err)
	}
	for _, id := range deleted {
		db.Sessions.DeleteForProfile(id)
	}
	out.People = len(deleted)
	db.logBestEffort(ctx, LogEntry{Category: LogAdmin, Action: "examples_removed", ActorID: actorLogID(actor),
		Summary: fmt.Sprintf("Removed the example data: %d items, %d people", out.Assets, out.People)})
	return out, nil
}

// ExamplesRemoved counts what RemoveExamples did.
type ExamplesRemoved struct {
	Assets int `json:"assets"`
	People int `json:"people"`
	// PeopleKept were example people still named in a real item's history.
	PeopleKept int `json:"people_kept"`
}
