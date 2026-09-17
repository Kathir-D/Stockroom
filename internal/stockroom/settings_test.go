package stockroom

import (
	"context"
	"errors"
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

// .env is a first-boot seed, not the source of truth. Once an admin has typed
// a value, a restart must not take it back.
func TestEnsureSettingsOnlySeedsNulls(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	restore := withTestSettings(t, db, admin, SettingsInput{BackupDir: strPtr("/chosen/by/the/admin")})
	defer restore()

	if _, err := db.EnsureSettings(ctx, Config{BackupDir: "/from/dot/env"}); err != nil {
		t.Fatalf("EnsureSettings: %v", err)
	}
	after, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.BackupDir != "/chosen/by/the/admin" {
		t.Errorf("backup_dir is %q after a restart; the environment overwrote what an admin set", after.BackupDir)
	}

	// A blank column, though, is exactly what the seed is for.
	if _, err := db.SaveSettings(ctx, admin, SettingsInput{BackupDir: strPtr("")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSettings(ctx, Config{BackupDir: "/from/dot/env"}); err != nil {
		t.Fatal(err)
	}
	seeded, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.BackupDir != "/from/dot/env" {
		t.Errorf("backup_dir is %q; an unset folder was not seeded from the environment", seeded.BackupDir)
	}
}

func intPtr(n int) *int    { return &n }
func boolPtr(b bool) *bool { return &b }
