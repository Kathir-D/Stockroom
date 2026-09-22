package stockroom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Backup settings live in the database so no admin ever edits a file
// (docs/design/backup.md §C.2). What these check is the gate, the bounds that
// turn a bad number into a readable 400, and the rule that a secret is never
// handed back out.

func TestSettingsAreAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))

	if _, err := db.GetSettings(ctx, student); !errors.Is(err, ErrForbidden) {
		t.Errorf("GetSettings as a student = %v, want ErrForbidden", err)
	}
	if _, err := db.SaveSettings(ctx, student, SettingsInput{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("SaveSettings as a student = %v, want ErrForbidden", err)
	}
	if _, err := db.BackupStatus(ctx, student); !errors.Is(err, ErrForbidden) {
		t.Errorf("BackupStatus as a student = %v, want ErrForbidden", err)
	}
}

// A secret comes back blank with a boolean beside it, never masked with
// asterisks: a masked value round-trips, and the panel would eventually write
// the mask back as the literal new token.
func TestSettingsNeverHandBackASecret(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	restore := withTestSettings(t, db, admin, SettingsInput{GitHubToken: strPtr("github_pat_secret")})
	defer restore()

	got, err := db.GetSettings(ctx, admin)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.GitHubToken != "" {
		t.Errorf("GetSettings returned the token %q; it must never leave the server", got.GitHubToken)
	}
	if !got.GitHubTokenSet {
		t.Error("GetSettings does not report that a token is stored, so the panel cannot tell configured from unconfigured")
	}

	// And an omitted secret leaves the stored one alone, which is what makes
	// saving any other field safe.
	if _, err := db.SaveSettings(ctx, admin, SettingsInput{ScheduleHour: intPtr(3)}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	raw, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if raw.GitHubToken != "github_pat_secret" {
		t.Errorf("the stored token is now %q; saving an unrelated field cleared it", raw.GitHubToken)
	}
}

// The bounds are repeated outside the DDL so a bad number is a 400 naming the
// field rather than a 500 carrying a constraint name. The split over which
// values accept zero is the argument in §C.2: a zero threshold is a coherent
// "never warn me", a zero interval or count is an always-on failure.
func TestSettingsBoundsAreReadable(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	cases := []struct {
		name  string
		in    SettingsInput
		field string
	}{
		{"zero retention", SettingsInput{KeepDays: intPtr(0)}, "keep_days"},
		{"zero staleness", SettingsInput{StaleHours: intPtr(0)}, "stale_hours"},
		{"hour 24", SettingsInput{ScheduleHour: intPtr(24)}, "schedule_hour"},
		{"negative headroom", SettingsInput{PhotoMinFreeGB: intPtr(-1)}, "photo_min_free_gb"},
		{"zero generations", SettingsInput{PhotoMaxGenerations: intPtr(0)}, "photo_max_generations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.SaveSettings(ctx, admin, tc.in)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.field) {
				t.Errorf("SaveSettings(%s) = %v, want ErrInvalid naming %s", tc.name, err, tc.field)
			}
		})
	}

	// Midnight and a zero free-space threshold are ordinary, not bugs.
	restore := withTestSettings(t, db, admin, SettingsInput{ScheduleHour: intPtr(0), PhotoMinFreeGB: intPtr(0)})
	restore()
}

// A folder typed without its leading separator is the mistake that produces a
// backup which reports success and cannot be found: the server resolves it
// against its own working directory, writes a complete, valid archive there,
// and every screen agrees it worked. Refuse it beside the field instead.
func TestBackupFolderMustBeAFullPath(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	for _, tc := range []struct {
		name  string
		in    SettingsInput
		field string
	}{
		{"a path missing its leading slash", SettingsInput{BackupDir: strPtr("Users/you/Desktop/backups")}, "backup_dir"},
		{"a relative folder", SettingsInput{BackupDir: strPtr("./backups")}, "backup_dir"},
		{"the photo mirror too", SettingsInput{PhotoBackupDir: strPtr("photos")}, "photo_backup_dir"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.SaveSettings(ctx, admin, tc.in)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.field) {
				t.Errorf("SaveSettings(%s) = %v, want ErrInvalid naming %s", tc.name, err, tc.field)
			}
		})
	}

	// An absolute folder is ordinary, and clearing one stays allowed: blank
	// means "not configured", which is a 503 the admin can act on, not a typo.
	//
	// withTestSettings wraps *both* writes, so the folder this database was
	// configured with is put back. A test that clears shared configuration and
	// leaves it cleared turns the next real backup into a 503, which is how
	// this comment came to be written.
	restore := withTestSettings(t, db, admin, SettingsInput{BackupDir: strPtr(t.TempDir())})
	defer restore()
	if _, err := db.SaveSettings(ctx, admin, SettingsInput{BackupDir: strPtr("")}); err != nil {
		t.Errorf("clearing the backup folder = %v, want nil", err)
	}
}

// Enabling a target with nothing to reach is a configuration that can only
// fail at 2 a.m. Say so while somebody is looking at the form.
func TestSettingsRefuseAHalfConfiguredTarget(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	_, err := db.SaveSettings(ctx, admin, SettingsInput{GitHubEnabled: boolPtr(true), GitHubRepo: strPtr("")})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("enabling GitHub with no repository = %v, want ErrInvalid", err)
	}
	_, err = db.SaveSettings(ctx, admin, SettingsInput{GitHubRepo: strPtr("not-a-repo")})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "owner/repository") {
		t.Errorf("a repository with no owner = %v, want ErrInvalid explaining owner/repository", err)
	}
}

// .env is a first-boot seed, not the source of truth. It fills a blank column
// once; after that neither a value an admin typed nor one an admin cleared may
// be taken back by a restart.
func TestEnsureSettingsSeedsOnceThenLeavesTheAdminAlone(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	// env_seeded is one-shot and per-database, so this test spends it. Put it
	// back on the way out, and clear it on the way in, so the test describes a
	// fresh install regardless of what ran before it.
	var seededBefore bool
	if err := db.Pool.QueryRow(ctx, `select env_seeded from app_settings where id = true`).Scan(&seededBefore); err != nil {
		t.Fatalf("read env_seeded: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `update app_settings set env_seeded = $1 where id = true`, seededBefore)
	})
	setSeeded := func(v bool) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `update app_settings set env_seeded = $1 where id = true`, v); err != nil {
			t.Fatalf("set env_seeded=%v: %v", v, err)
		}
	}

	restore := withTestSettings(t, db, admin, SettingsInput{BackupDir: strPtr("")})
	defer restore()

	// First boot against a blank column: this is what the seed is for.
	setSeeded(false)
	if _, err := db.EnsureSettings(ctx, Config{BackupDir: "/from/dot/env"}); err != nil {
		t.Fatalf("EnsureSettings: %v", err)
	}
	seeded, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.BackupDir != "/from/dot/env" {
		t.Errorf("backup_dir is %q; an unset folder was not seeded from the environment", seeded.BackupDir)
	}

	// The admin now clears it. A restart must leave it cleared: before the
	// env_seeded marker this is exactly where .env reinstated the value and
	// silently undid the change.
	if _, err := db.SaveSettings(ctx, admin, SettingsInput{BackupDir: strPtr("")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSettings(ctx, Config{BackupDir: "/from/dot/env"}); err != nil {
		t.Fatal(err)
	}
	cleared, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.BackupDir != "" {
		t.Errorf("backup_dir is %q after a restart; the environment reinstated a setting the admin had cleared", cleared.BackupDir)
	}

	// And the original guard still holds on its own: even on a database that
	// has not been seeded yet, a column an admin has filled in is not null, so
	// the seed passes over it.
	if _, err := db.SaveSettings(ctx, admin, SettingsInput{BackupDir: strPtr("/chosen/by/the/admin")}); err != nil {
		t.Fatal(err)
	}
	setSeeded(false)
	if _, err := db.EnsureSettings(ctx, Config{BackupDir: "/from/dot/env"}); err != nil {
		t.Fatal(err)
	}
	kept, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kept.BackupDir != "/chosen/by/the/admin" {
		t.Errorf("backup_dir is %q after a restart; the environment overwrote what an admin set", kept.BackupDir)
	}
}

func intPtr(n int) *int    { return &n }
func boolPtr(b bool) *bool { return &b }

// "Test connection" exists to name a misconfiguration. Its failure therefore
// has to reach the admin as words, not as a 500.
//
// Measured before the fix (2026-09-18): a wrong GitHub token answered
// `500 {"error":"internal error"}` while the server log held the real reason,
// "github 401 Unauthorized: Bad credentials" — writeError's default branch logs
// and hides anything without a sentinel. A target that says "bad credentials"
// is a setting that is wrong, so it is ErrInvalid and the message survives.
func TestTargetTestFailureCarriesItsReason(t *testing.T) {
	err := wrapTargetTest(errors.New("github 401 Unauthorized: Bad credentials"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a failed connection test = %v, want ErrInvalid so the message survives", err)
	}
	if !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("the reason was lost on the way out: %q", err.Error())
	}

	// A multi-line failure (rclone writes several) is flattened, because the
	// panel shows it in one line.
	err = wrapTargetTest(errors.New("rclone failed\ndirectory not found\n"))
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("a multi-line failure reached the API with its newlines: %q", err.Error())
	}

	// A sentinel the target already chose is passed through, so "not set up
	// yet" stays a 503 with its own wording instead of becoming a 400.
	notConfigured := fmt.Errorf("%w: Google Drive backups are not set up", ErrNotConfigured)
	if got := wrapTargetTest(notConfigured); !errors.Is(got, ErrNotConfigured) || errors.Is(got, ErrInvalid) {
		t.Errorf("ErrNotConfigured was rewritten as %v", got)
	}

	if wrapTargetTest(nil) != nil {
		t.Error("a successful test produced an error")
	}
}

// The photo wall's folder joined the .env seed a day after env_seeded had
// already been set true on every database that had started the server once.
// Sharing that marker gave the folder a WHERE clause that is false on exactly
// the installations it was written for: SIGNIN_PHOTOS_FOLDER_ID would pre-fill
// a database created after the feature and be silently ignored on every one
// created before it. It has its own marker, so this is the same first-boot
// seed on an upgraded machine as on a fresh one.
func TestEnsureSettingsSeedsThePhotoWallFolderOnAnUpgradedDatabase(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	defer restorePhotoWallFolder(t, db)()

	// Both markers are one-shot and per-database, so this test spends them.
	// Put them back on the way out.
	var envBefore, photoBefore bool
	if err := db.Pool.QueryRow(ctx,
		`select env_seeded, signin_photos_env_seeded from app_settings where id = true`).
		Scan(&envBefore, &photoBefore); err != nil {
		t.Fatalf("read the seed markers: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(),
			`update app_settings set env_seeded = $1, signin_photos_env_seeded = $2 where id = true`,
			envBefore, photoBefore)
	})
	setMarkers := func(env, photo bool) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `
			update app_settings set
				env_seeded = $1, signin_photos_env_seeded = $2,
				signin_photos_folder_id = null, signin_photos_label = null
			where id = true`, env, photo); err != nil {
			t.Fatalf("set the seed markers: %v", err)
		}
	}
	folder := func() photoWallFolder {
		t.Helper()
		f, err := db.loadPhotoWallFolder(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	restore := withTestSettings(t, db, admin, SettingsInput{BackupDir: strPtr("/chosen/by/the/admin")})
	defer restore()

	// The upgrade: the backup values were seeded by an earlier release, so
	// env_seeded is already true. Before the second marker this start-up
	// copied nothing and said nothing.
	setMarkers(true, false)
	if _, err := db.EnsureSettings(ctx, Config{
		BackupDir:            "/from/dot/env",
		SignInPhotosFolderID: "FOLDER-FROM-ENV",
	}); err != nil {
		t.Fatalf("EnsureSettings: %v", err)
	}
	if got := folder(); got.id != "FOLDER-FROM-ENV" {
		t.Errorf("signin_photos_folder_id is %q; .env was ignored on a database that had already seeded its backup settings", got.id)
	} else if got.label == "" {
		t.Error("a seeded folder is nameless on the admin screen; the label should ride along with it")
	}

	// And the pass that just ran must not have re-opened the backup columns:
	// an admin who cleared one yesterday does not get it back because the
	// photo wall gained a seed today.
	s, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.BackupDir != "/chosen/by/the/admin" {
		t.Errorf("backup_dir is %q; the photo wall's pass reached a column that is not its own", s.BackupDir)
	}

	// The admin then replaces the folder through the panel. A restart must
	// leave theirs alone -- the whole point of the marker, and the case the
	// backup columns' own seed never meets because it runs before anyone can
	// have typed anything.
	// savePhotoWallFolder rather than SetPhotoWallFolder: the exported one
	// probes Drive first (§7), and this test has no rclone and no folder.
	if err := db.savePhotoWallFolder(ctx, admin, "CHOSEN-BY-THE-ADMIN", "Fall 2026 game photos"); err != nil {
		t.Fatalf("savePhotoWallFolder: %v", err)
	}
	if _, err := db.EnsureSettings(ctx, Config{SignInPhotosFolderID: "FOLDER-FROM-ENV"}); err != nil {
		t.Fatal(err)
	}
	if got := folder(); got.id != "CHOSEN-BY-THE-ADMIN" {
		t.Errorf("signin_photos_folder_id is %q after a restart; the environment overwrote the folder an admin chose", got.id)
	}

	// The marker is set even when .env is blank: what is one-time is the
	// pass, not the copying. Otherwise a folder deliberately left unset today
	// would be filled in by a restart after somebody edits .env tomorrow.
	setMarkers(true, false)
	if _, err := db.EnsureSettings(ctx, Config{}); err != nil {
		t.Fatal(err)
	}
	var marked bool
	if err := db.Pool.QueryRow(ctx,
		`select signin_photos_env_seeded from app_settings where id = true`).Scan(&marked); err != nil {
		t.Fatal(err)
	}
	if !marked {
		t.Fatal("a blank SIGNIN_PHOTOS_FOLDER_ID left the pass unspent")
	}
	if _, err := db.EnsureSettings(ctx, Config{SignInPhotosFolderID: "LATE-ARRIVAL"}); err != nil {
		t.Fatal(err)
	}
	if got := folder(); got.id != "" {
		t.Errorf("signin_photos_folder_id is %q; .env seeded a folder on a later restart, after its one pass had already run", got.id)
	}
}

// The refusal above guards the field an admin types into. This one guards the
// run, because a database configured before that refusal shipped still holds a
// relative folder and no migration rewrites it -- and the whole point of the
// decision is that such a run *succeeds*, writing a valid archive somewhere
// nobody will look. A folder that is not a full path is the unconfigured
// state, which the backup screen and every sign-in already say out loud.
func TestAStoredRelativeFolderIsNotUsedAtRunTime(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	before, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	// Written past SaveSettings on purpose: validateDir would refuse these,
	// which is exactly why they can only arrive from an older installation.
	if _, err := db.Pool.Exec(ctx,
		`update app_settings set backup_dir = $1, photo_backup_dir = $2 where id = true`,
		"Users/you/Desktop/backups", "Users/you/Desktop/photos"); err != nil {
		t.Fatalf("plant the old values: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx,
			`update app_settings set backup_dir = $1, photo_backup_dir = $2 where id = true`,
			nullable(before.BackupDir), nullable(before.PhotoBackupDir))
	})

	overrideBackup, overridePhotos := db.BackupDir, db.PhotoBackupDir
	db.BackupDir, db.PhotoBackupDir = "", ""
	t.Cleanup(func() { db.BackupDir, db.PhotoBackupDir = overrideBackup, overridePhotos })

	if _, _, err := db.backupDir(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("backupDir with a relative folder = %v, want ErrNotConfigured", err)
	}

	planted, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if dir := db.photoBackupDir(planted); dir != "" {
		t.Errorf("photoBackupDir with a relative folder = %q, want \"\"", dir)
	}

	// And it says so, rather than mirroring nothing in silence.
	status, err := db.PhotoMirrorStatus(ctx, planted)
	if err != nil {
		t.Fatalf("PhotoMirrorStatus: %v", err)
	}
	if len(status.Warnings) == 0 || !strings.Contains(status.Warnings[0], "full path") {
		t.Errorf("PhotoMirrorStatus warnings = %v, want one naming the folder", status.Warnings)
	}
}
