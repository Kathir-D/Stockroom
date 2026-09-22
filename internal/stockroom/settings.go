package stockroom

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Backup configuration, which lives in the database rather than in .env
// (docs/design/backup.md §C.2). The rule it exists to satisfy is short: after
// the one-time software install, nobody edits a file. Changing where backups
// go, how long they are kept, or which Google account they are pushed to is a
// form in the admin panel.
//
// .env keeps exactly one job -- seeding these columns the first time the
// server starts against a fresh database (EnsureSettings). After that the
// database is the source of truth and the environment is ignored, so an
// installer can pre-fill the machine without the environment quietly winning
// against something an admin typed later.

// Settings is the whole of app_settings, one row.
//
// The two secrets are never populated by the read path that reaches HTTP: see
// Redacted, which blanks them and reports only whether they are set. A UI that
// renders a token can leak it into a screenshot, a support email, or a browser
// extension, and the admin panel has no reason to ever show one back.
type Settings struct {
	BackupDir      string `json:"backup_dir"`
	PhotoBackupDir string `json:"photo_backup_dir"`

	KeepDays     int `json:"keep_days"`
	StaleHours   int `json:"stale_hours"`
	ScheduleHour int `json:"schedule_hour"`

	DriveEnabled bool   `json:"drive_enabled"`
	DriveRemote  string `json:"drive_remote"`
	DrivePath    string `json:"drive_path"`

	GitHubEnabled bool   `json:"github_enabled"`
	GitHubRepo    string `json:"github_repo"`
	GitHubToken   string `json:"github_token"`

	ArchivePassphrase string `json:"archive_passphrase"`

	PhotoMinFreeGB      int `json:"photo_min_free_gb"`
	PhotoMaxGenerations int `json:"photo_max_generations"`

	// What a student number may look like (student_number.go). Not a backup
	// setting, but app_settings is already "the things an admin configures
	// without opening a file", and a second settings table would be a second
	// screen for one field.
	StudentNumberFormat  string `json:"student_number_format"`
	StudentNumberPattern string `json:"student_number_pattern"`

	UpdatedAt time.Time `json:"updated_at"`

	// GitHubTokenSet and ArchivePassphraseSet are the only thing the API says
	// about a secret: whether there is one. They are derived by Redacted and
	// are not columns.
	GitHubTokenSet       bool `json:"github_token_set"`
	ArchivePassphraseSet bool `json:"archive_passphrase_set"`
}

// Encrypted reports whether archives leave this machine encrypted (§C.5).
func (s Settings) Encrypted() bool { return s.ArchivePassphrase != "" }

// Redacted is Settings as the HTTP layer may see it: the secrets blanked, with
// a boolean in their place.
//
// Blanking rather than masking with a run of asterisks is deliberate. A masked
// value round-trips: the panel renders "********", the admin edits some other
// field, saves, and the mask is written back as the literal new token. Every
// system that has ever done this has produced the same outage. The field comes
// back empty, and SaveSettings reads an omitted secret as "leave it alone".
func (s Settings) Redacted() Settings {
	s.GitHubTokenSet = s.GitHubToken != ""
	s.ArchivePassphraseSet = s.ArchivePassphrase != ""
	s.GitHubToken = ""
	s.ArchivePassphrase = ""
	return s
}

// SettingsInput is a partial update: every field is a pointer, and a nil one
// leaves the column as it is. That is what lets the settings screen save one
// card without carrying -- and possibly clobbering -- the values on the other
// cards.
//
// For the two secrets, nil means "unchanged" and an empty string means
// "remove it". Those have to be distinguishable, which is the whole reason for
// the pointers.
type SettingsInput struct {
	BackupDir      *string `json:"backup_dir"`
	PhotoBackupDir *string `json:"photo_backup_dir"`

	KeepDays     *int `json:"keep_days"`
	StaleHours   *int `json:"stale_hours"`
	ScheduleHour *int `json:"schedule_hour"`

	DriveEnabled *bool   `json:"drive_enabled"`
	DriveRemote  *string `json:"drive_remote"`
	DrivePath    *string `json:"drive_path"`

	GitHubEnabled *bool   `json:"github_enabled"`
	GitHubRepo    *string `json:"github_repo"`
	GitHubToken   *string `json:"github_token"`

	ArchivePassphrase *string `json:"archive_passphrase"`

	PhotoMinFreeGB      *int `json:"photo_min_free_gb"`
	PhotoMaxGenerations *int `json:"photo_max_generations"`

	StudentNumberFormat  *string `json:"student_number_format"`
	StudentNumberPattern *string `json:"student_number_pattern"`
}

const settingsColumns = `backup_dir, photo_backup_dir, keep_days, stale_hours, schedule_hour,
	drive_enabled, drive_remote, drive_path,
	github_enabled, github_repo, github_token, archive_passphrase,
	photo_min_free_gb, photo_max_generations,
	student_number_format, student_number_pattern, updated_at`

func scanSettings(row pgx.Row) (Settings, error) {
	var s Settings
	// Every text column is nullable, so they scan through pointers and become
	// "" rather than making every caller test for nil. Nothing downstream
	// distinguishes an unset path from a blank one.
	var backupDir, photoDir, driveRemote, drivePath, repo, token, passphrase *string
	var snPattern *string
	err := row.Scan(&backupDir, &photoDir, &s.KeepDays, &s.StaleHours, &s.ScheduleHour,
		&s.DriveEnabled, &driveRemote, &drivePath,
		&s.GitHubEnabled, &repo, &token, &passphrase,
		&s.PhotoMinFreeGB, &s.PhotoMaxGenerations,
		&s.StudentNumberFormat, &snPattern, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, ErrNotFound
	}
	if err != nil {
		return Settings{}, fmt.Errorf("scan settings: %w", err)
	}
	s.StudentNumberPattern = deref(snPattern)
	s.BackupDir = deref(backupDir)
	s.PhotoBackupDir = deref(photoDir)
	s.DriveRemote = deref(driveRemote)
	s.DrivePath = deref(drivePath)
	s.GitHubRepo = deref(repo)
	s.GitHubToken = deref(token)
	s.ArchivePassphrase = deref(passphrase)
	return s, nil
}

// nullable turns a trimmed value into what the column should hold: nil for
// blank, so "unset" is null in the database and not an empty string that reads
// as configured.
func nullable(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// loadSettings reads the one row, secrets and all. Unexported because a secret
// must not reach the HTTP layer by accident: GetSettings is the exported read
// and it redacts.
func (db *DB) loadSettings(ctx context.Context) (Settings, error) {
	s, err := scanSettings(db.Pool.QueryRow(ctx, `select `+settingsColumns+` from app_settings where id = true`))
	if errors.Is(err, ErrNotFound) {
		// The migration inserts the row, so this is a database that has had it
		// deleted by hand. Recreate rather than fail: every default is in the
		// DDL, and a backup system that refuses to run because a settings row
		// is missing is worse than one that recreates it.
		if _, ierr := db.Pool.Exec(ctx, `insert into app_settings (id) values (true) on conflict (id) do nothing`); ierr != nil {
			return Settings{}, fmt.Errorf("recreate settings row: %w", ierr)
		}
		return scanSettings(db.Pool.QueryRow(ctx, `select `+settingsColumns+` from app_settings where id = true`))
	}
	return s, err
}

// GetSettings is the admin panel's read. The secrets come back blank with a
// boolean saying whether one is stored.
func (db *DB) GetSettings(ctx context.Context, actor Actor) (Settings, error) {
	if err := RequireAdmin(actor); err != nil {
		return Settings{}, err
	}
	s, err := db.loadSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return s.Redacted(), nil
}

// SaveSettings applies a partial update and returns the redacted result.
//
// Every bound the DDL carries is checked here first, so a number outside it is
// a 400 naming the field and saying what the range is, rather than a 500
// carrying a Postgres constraint name that means nothing to the person reading
// it. The database keeps its constraints anyway: this is the readable copy,
// not the only one.
//
// The read and the write are one locked transaction because this is a
// read-modify-write over a single row and the input is a *partial* update.
// Unlocked, two admins saving different tabs at the same time each read the
// same row, each fill in the fields they were not editing from that stale
// copy, and whichever writes second silently reverts the other's change --
// with both screens reporting success. `for update` makes the second save
// wait and re-read, so it merges onto the first instead of over it.
func (db *DB) SaveSettings(ctx context.Context, actor Actor, in SettingsInput) (Settings, error) {
	if err := RequireAdmin(actor); err != nil {
		return Settings{}, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return Settings{}, fmt.Errorf("save settings: %w", err)
	}
	defer tx.Rollback(ctx)

	cur, err := lockSettings(ctx, tx)
	if err != nil {
		return Settings{}, err
	}

	next := cur
	if in.BackupDir != nil {
		next.BackupDir = strings.TrimSpace(*in.BackupDir)
		if err := validateDir("backup_dir", next.BackupDir); err != nil {
			return Settings{}, err
		}
	}
	if in.PhotoBackupDir != nil {
		next.PhotoBackupDir = strings.TrimSpace(*in.PhotoBackupDir)
		if err := validateDir("photo_backup_dir", next.PhotoBackupDir); err != nil {
			return Settings{}, err
		}
	}
	if in.KeepDays != nil {
		next.KeepDays = *in.KeepDays
	}
	if in.StaleHours != nil {
		next.StaleHours = *in.StaleHours
	}
	if in.ScheduleHour != nil {
		next.ScheduleHour = *in.ScheduleHour
	}
	if in.DriveEnabled != nil {
		next.DriveEnabled = *in.DriveEnabled
	}
	if in.DriveRemote != nil {
		next.DriveRemote = strings.TrimSpace(*in.DriveRemote)
	}
	if in.DrivePath != nil {
		next.DrivePath = strings.TrimSpace(*in.DrivePath)
	}
	if in.GitHubEnabled != nil {
		next.GitHubEnabled = *in.GitHubEnabled
	}
	if in.GitHubRepo != nil {
		next.GitHubRepo = strings.TrimSpace(*in.GitHubRepo)
	}
	if in.GitHubToken != nil {
		next.GitHubToken = strings.TrimSpace(*in.GitHubToken)
	}
	if in.ArchivePassphrase != nil {
		// Not trimmed: a passphrase is the one value where a leading space is
		// a character the person chose, and silently eating it makes the
		// archive undecryptable with the passphrase they wrote down.
		next.ArchivePassphrase = *in.ArchivePassphrase
	}
	if in.PhotoMinFreeGB != nil {
		next.PhotoMinFreeGB = *in.PhotoMinFreeGB
	}
	if in.PhotoMaxGenerations != nil {
		next.PhotoMaxGenerations = *in.PhotoMaxGenerations
	}
	if in.StudentNumberFormat != nil {
		next.StudentNumberFormat = strings.TrimSpace(*in.StudentNumberFormat)
	}
	if in.StudentNumberPattern != nil {
		next.StudentNumberPattern = strings.TrimSpace(*in.StudentNumberPattern)
	}

	// Compiled and checked before the write; *installed* after the commit.
	// A pattern that does not compile locks every account out of sign-in, and
	// the way back in is the failsafe admin, whose number has to satisfy the
	// same rule -- so it is proven here. Installing it here too would mean a
	// save whose transaction then failed had silently changed how sign-in
	// behaves until the next restart.
	if err := ValidateStudentNumberFormat(
		StudentNumberFormat(next.StudentNumberFormat), next.StudentNumberPattern,
	); err != nil {
		return Settings{}, err
	}

	if err := next.validate(); err != nil {
		return Settings{}, err
	}

	// rclone's remote name is the left half of "remote:path" and the target
	// enables on it, so an enabled Drive with no remote is a configuration
	// that can only fail at 2 a.m. Say so now, while somebody is looking.
	if next.DriveEnabled && next.DriveRemote == "" {
		return Settings{}, fmt.Errorf("%w: drive_remote is required to enable Google Drive backups; press Connect to set one up", ErrInvalid)
	}
	if next.GitHubEnabled {
		if next.GitHubRepo == "" {
			return Settings{}, fmt.Errorf("%w: github_repo is required to enable GitHub backups, as owner/repository", ErrInvalid)
		}
		if next.GitHubToken == "" {
			return Settings{}, fmt.Errorf("%w: a GitHub token is required to enable GitHub backups", ErrInvalid)
		}
	}
	if next.GitHubRepo != "" {
		if err := validateRepo(next.GitHubRepo); err != nil {
			return Settings{}, err
		}
	}

	_, err = tx.Exec(ctx, `
		update app_settings set
			backup_dir = $1, photo_backup_dir = $2,
			keep_days = $3, stale_hours = $4, schedule_hour = $5,
			drive_enabled = $6, drive_remote = $7, drive_path = $8,
			github_enabled = $9, github_repo = $10, github_token = $11,
			archive_passphrase = $12,
			photo_min_free_gb = $13, photo_max_generations = $14,
			student_number_format = $15, student_number_pattern = $16,
			updated_at = now()
		where id = true`,
		nullable(next.BackupDir), nullable(next.PhotoBackupDir),
		next.KeepDays, next.StaleHours, next.ScheduleHour,
		next.DriveEnabled, nullable(next.DriveRemote), nullable(next.DrivePath),
		next.GitHubEnabled, nullable(next.GitHubRepo), nullable(next.GitHubToken),
		nullable(next.ArchivePassphrase),
		next.PhotoMinFreeGB, next.PhotoMaxGenerations,
		next.StudentNumberFormat, nullable(next.StudentNumberPattern))
	if err != nil {
		return Settings{}, mapPgError("save settings", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Settings{}, fmt.Errorf("save settings: %w", err)
	}

	// Now that the row is durable, make the running process agree with it.
	// Validated above, so this cannot fail; logged rather than ignored if it
	// somehow does, because the alternative is a database and a process that
	// disagree about who may sign in.
	if err := SetStudentNumberFormat(
		StudentNumberFormat(next.StudentNumberFormat), next.StudentNumberPattern,
	); err != nil {
		log.Printf("warning: saved student-number format %q but could not apply it: %v",
			next.StudentNumberFormat, err)
	}
	return db.GetSettings(ctx, actor)
}

// lockSettings is loadSettings for a caller inside a transaction that is about
// to write: it takes a row lock, so the read it returns is still true at the
// moment of the update.
//
// The missing-row recreate mirrors loadSettings, for the same reason given
// there -- a settings row deleted by hand is recreated from the DDL's defaults
// rather than failing the request.
func lockSettings(ctx context.Context, q querier) (Settings, error) {
	const sel = `select ` + settingsColumns + ` from app_settings where id = true for update`
	s, err := scanSettings(q.QueryRow(ctx, sel))
	if errors.Is(err, ErrNotFound) {
		if _, ierr := q.Exec(ctx, `insert into app_settings (id) values (true) on conflict (id) do nothing`); ierr != nil {
			return Settings{}, fmt.Errorf("recreate settings row: %w", ierr)
		}
		return scanSettings(q.QueryRow(ctx, sel))
	}
	return s, err
}

// validate repeats the DDL's bounds with a message an admin can act on. The
// split between which values accept zero is argued in the migration and in
// docs/design/backup.md §C.2: a zero *threshold* is a coherent "never warn
// me", a zero *interval* or *count* is an always-on failure.
func (s Settings) validate() error {
	switch {
	case s.KeepDays < 1:
		return fmt.Errorf("%w: keep_days must be at least 1 day; 0 would start a fresh photo generation on every run and prune the backup it just wrote", ErrInvalid)
	case s.StaleHours < 1:
		return fmt.Errorf("%w: stale_hours must be at least 1 hour; 0 makes every run stale the instant it finishes, so the warning never turns off", ErrInvalid)
	case s.ScheduleHour < 0 || s.ScheduleHour > 23:
		return fmt.Errorf("%w: schedule_hour must be between 0 (midnight) and 23, got %d", ErrInvalid, s.ScheduleHour)
	case s.PhotoMinFreeGB < 0:
		return fmt.Errorf("%w: photo_min_free_gb cannot be negative; 0 turns the free-space warning off", ErrInvalid)
	case s.PhotoMaxGenerations < 1:
		return fmt.Errorf("%w: photo_max_generations must be at least 1", ErrInvalid)
	}
	return nil
}

// validateDir refuses a folder that is not a full path.
//
// It is called from SaveSettings per field rather than from validate(), which
// re-checks the whole merged row: every card on the settings screen saves on
// its own (§13, 2026-09-17), so a bad value left in one card must not be able
// to refuse an unrelated save in another and report it against a field the
// admin cannot see from there.
func validateDir(field, dir string) error {
	if dir == "" || filepath.IsAbs(dir) {
		return nil
	}
	return fmt.Errorf("%w: %s must be a full path, %s, got %q", ErrInvalid, field, absHint, dir)
}

// absHint says what a full path looks like on this machine. "Must be absolute"
// is jargon; the admin is reading a Finder or Explorer window, and the mistake
// this catches is copying a path out of one and losing the leading separator.
//
// Why a relative folder is refused rather than resolved: it is interpreted
// against whatever directory the server happened to be started in, which is
// the repository root under `dev.sh`, `System32` under a Windows service, and
// `/` under launchd. The run then succeeds -- files are written, the manifest
// checks out, every screen says the backup worked -- while the archive sits
// somewhere nobody will ever look. That is precisely the failure mode this
// whole subsystem exists to make impossible (docs/design/backup.md §A), and it
// is worth one refusal beside the field to prevent.
var absHint = func() string {
	if runtime.GOOS == "windows" {
		return `starting with a drive letter, for example C:\Stockroom\backups`
	}
	return "starting with a slash, for example /Users/you/Stockroom-backups"
}()

// validateRepo checks the owner/repository shape the GitHub target pastes into
// its URLs. Catching it here turns a 404 from api.github.com at 2 a.m. into a
// message beside the field the admin is typing into.
func validateRepo(repo string) error {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("%w: github_repo must be owner/repository, for example %q, got %q", ErrInvalid, "your-username/stockroom-backup", repo)
	}
	return nil
}

// EnsureSettings seeds null columns from the environment on start-up and
// returns the settings in force.
//
// It runs exactly once per database, guarded by the env_seeded and
// signin_photos_env_seeded markers, and writes only where the column is null. That is the whole distinction in §C.2
// between "bootstrap fallback" and "source of truth", and getting it backwards
// would mean an admin's change silently reverting on the next restart.
//
// The null check alone was not enough to make it a *first*-boot seed, because
// clearing a field in the panel writes null (`nullable`): the next start-up
// saw an empty column, could not tell "never set" from "deliberately cleared",
// and restored the .env value. The marker records the fact instead of
// inferring it. It is set even when .env was blank and nothing was copied --
// the pass is what is one-time, not the copying -- so a value cleared today
// stays cleared after someone fills in .env tomorrow.
//
// One statement per pass, with the marker in the same UPDATE as the seed and
// `not <marker>` in the WHERE. Two servers racing to start therefore cannot
// both seed: the second finds the row already marked and updates nothing.
//
// There are two passes because there are two markers, and that is a fact
// about when the columns shipped rather than a design with a spare part. The
// photo wall's folder joined this seed a day after env_seeded had already
// been set true on every database that had started the server -- so carrying
// it in the first statement gave it a WHERE clause that is false on exactly
// the installations it was written for, and SIGNIN_PHOTOS_FOLDER_ID would be
// silently ignored on all of them. Its own marker, defaulting to false on a
// database that has never run the pass, is what makes it the first-boot seed
// CLAUDE.md §9 describes on an upgraded machine as well as a fresh one. See
// 20260921090000_app_settings_signin_photos_env_seeded.sql.
//
// The two are deliberately not one transaction: each is idempotent and
// guarded by its own marker, so a start-up that dies between them simply
// finishes the second pass on the next one.
func (db *DB) EnsureSettings(ctx context.Context, cfg Config) (Settings, error) {
	if _, err := db.loadSettings(ctx); err != nil {
		return Settings{}, err
	}
	_, err := db.Pool.Exec(ctx, `
		update app_settings set
			backup_dir       = coalesce(backup_dir,       nullif($1, '')),
			photo_backup_dir = coalesce(photo_backup_dir, nullif($2, '')),
			drive_remote     = coalesce(drive_remote,     nullif($3, '')),
			env_seeded       = true
		where id = true and not env_seeded`,
		absoluteDir(cfg.BackupDir), absoluteDir(cfg.PhotoBackupDir),
		strings.TrimSpace(cfg.RcloneRemote))
	if err != nil {
		return Settings{}, fmt.Errorf("seed settings from environment: %w", err)
	}

	// The photo wall's folder, on its own marker. coalesce keeps a folder an
	// admin has already chosen -- this pass is late on an upgraded database,
	// so unlike the statement above it can meet a column somebody has filled
	// in through the panel, and overwriting that from .env is the exact
	// reversal §C.2 rules out. The marker is set even when .env is blank,
	// because what is one-time is the pass and not the copying.
	_, err = db.Pool.Exec(ctx, `
		update app_settings set
			signin_photos_folder_id  = coalesce(signin_photos_folder_id, nullif($1, '')),
			-- The label rides along so a seeded folder is not nameless on the
			-- admin screen, and only when a folder is actually being put in:
			-- the right-hand side of an UPDATE reads the row as it was, so
			-- this tests the column before the line above fills it.
			signin_photos_label      = case
				when signin_photos_folder_id is null and nullif($1, '') is not null
				then coalesce(signin_photos_label, 'Folder from .env')
				else signin_photos_label
			end,
			signin_photos_env_seeded = true
		where id = true and not signin_photos_env_seeded`,
		strings.TrimSpace(cfg.SignInPhotosFolderID))
	if err != nil {
		return Settings{}, fmt.Errorf("seed the photo wall folder from environment: %w", err)
	}
	return db.loadSettings(ctx)
}

// absoluteDir resolves a folder from .env against the working directory, so the
// column ends up holding the same kind of value SaveSettings insists on. A
// relative BACKUP_DIR is reasonable in a developer's .env (`./backups`, beside
// the repo) and meaningless once the same binary is started by a service from
// a different directory; resolving it at seed time fixes the meaning to the
// place the person who wrote it meant.
//
// It never fails: EnsureSettings runs on the start-up path, and a folder that
// cannot be resolved must not be able to stop the API from starting, the same
// way a malformed failsafe admin does not (CLAUDE.md §7).
func absoluteDir(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// backupDir resolves where this run writes: the DB field first, the settings
// row second.
//
// The order matters and is the opposite of what it looks like. DB.BackupDir is
// an *explicit* override -- a test pointing at a temp directory -- while the
// settings row is the configured value the admin owns. main.go deliberately
// leaves the field empty so production always reads the row; if it passed
// .env through, every restart would quietly out-vote the settings screen.
func (db *DB) backupDir(ctx context.Context) (string, Settings, error) {
	s, err := db.loadSettings(ctx)
	if err != nil {
		return "", Settings{}, err
	}
	dir := db.BackupDir
	if dir == "" {
		dir = s.BackupDir
	}
	if dir == "" {
		return "", s, fmt.Errorf("%w: no backup folder is set. Open Admin → Settings and choose where backups should be written", ErrNotConfigured)
	}
	// The stored value is re-checked here, not only where it was typed.
	// validateDir has refused a relative folder at the settings screen since
	// 2026-09-21, but the database that produced that decision already held
	// one, and nothing rewrites a column on upgrade. Resolving it now against
	// whatever directory the server started in is exactly the silent success
	// the decision rules out -- a complete archive, a manifest that verifies,
	// every screen reporting a healthy backup, and the files somewhere nobody
	// looks. Refusing before the run starts makes it an unconfigured folder,
	// which the backup screen and the sign-in warning already say out loud.
	if !filepath.IsAbs(dir) {
		return "", s, fmt.Errorf("%w: the backup folder %q is not a full path, so which folder it means depends on where the server was started. Open Admin → Settings and enter one %s", ErrNotConfigured, dir, absHint)
	}
	return dir, s, nil
}

// photoBackupDir is backupDir's counterpart for the photo mirror. An unset
// photo folder is not an error at every call site -- MirrorPhotos treats it as
// "photos are not being mirrored" -- so this returns "" rather than
// ErrNotConfigured and lets each caller decide.
func (db *DB) photoBackupDir(s Settings) string {
	dir := db.PhotoBackupDir
	if dir == "" {
		dir = s.PhotoBackupDir
	}
	// A folder that is not a full path is the unconfigured state here too, for
	// backupDir's reason: a stored relative value predates validateDir, and a
	// mirror written to a folder that moves with the working directory is the
	// same silent success. "" is the answer every caller already handles, and
	// PhotoMirrorStatus says which of the two empties this is.
	if !filepath.IsAbs(dir) {
		return ""
	}
	return dir
}
