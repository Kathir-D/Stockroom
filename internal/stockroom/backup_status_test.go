package stockroom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Failure visibility (docs/design/backup.md §E.7). A backup system fails
// silently by default; these check that it does not.

func TestBackupStatusReportsStaleness(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()

	// A run that succeeded five days ago, written the way a real run writes it.
	fiveDaysAgo := time.Now().AddDate(0, 0, -5)
	writeBackupState(db.BackupDir, backupState{
		Targets:   map[string]targetState{localTarget: {LastSuccess: &fiveDaysAgo}},
		LastRunAt: &fiveDaysAgo,
	})

	status, err := db.BackupStatus(ctx, admin)
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	if !status.Stale {
		t.Fatal("a backup five days old is not reported as stale")
	}
	if !containsSubstring(status.Warnings, "5 days") {
		t.Errorf("the warnings do not say how long it has been: %v", status.Warnings)
	}

	// The same fact, worded for each audience. A student is told who to tell,
	// by name -- never by student number, which is a working scan login.
	invalidateBackupWarning()
	studentWarning := db.backupWarningFor(ctx, false)
	adminWarning := db.backupWarningFor(ctx, true)
	if studentWarning == nil || adminWarning == nil {
		t.Fatal("a stale backup produced no sign-in warning for somebody")
	}
	if !strings.Contains(adminWarning.Message, "Admin") {
		t.Errorf("the admin's warning does not say where to go: %q", adminWarning.Message)
	}
	for _, name := range studentWarning.Admins {
		if strings.ContainsAny(name, "0123456789") {
			t.Errorf("an admin is named to a student as %q, which looks like a student number", name)
		}
	}
}

// The failsafe admin is best-effort by decision, which means a site that never
// filled in .env restores a wiped database into zero accounts and finds the
// admin panel unreachable. The warning has to fire while somebody is still
// signed in to read it.
func TestBackupStatusWarnsWithNoFailsafeAdmin(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()

	SetFailsafeAdminConfigured(false)
	t.Cleanup(func() { SetFailsafeAdminConfigured(false) })

	status, err := db.BackupStatus(ctx, admin)
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	if status.FailsafeAdminConfigured {
		t.Fatal("BackupStatus claims a failsafe admin is configured when none was recorded")
	}
	if !containsSubstring(status.Warnings, "failsafe admin") {
		t.Errorf("no warning about the missing failsafe admin: %v", status.Warnings)
	}

	SetFailsafeAdminConfigured(true)
	status, err = db.BackupStatus(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if containsSubstring(status.Warnings, "failsafe admin") {
		t.Errorf("the failsafe warning still fires once one is configured: %v", status.Warnings)
	}
}

// A run writes down what happened, so the backup screen and the next boot's
// catch-up have something to read.
func TestRecordBackupRunWritesStateAndLog(t *testing.T) {
	db := requireTestDB(t)
	dir := t.TempDir()
	ranAt := time.Now()

	db.recordBackupRun(dir, BackupResult{
		RanAt:  ranAt,
		Source: BackupSourceScheduled,
		Rows:   42,
		Tables: []TableExport{{Table: "assets", Rows: 42}},
		Targets: []TargetResult{
			{Target: githubTargetName, OK: true, Ref: "abc123"},
			{Target: driveTargetName, Error: "rclone: no such remote"},
		},
	}, nil)

	raw, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatalf("no state file was written: %v", err)
	}
	var state backupState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if state.Targets[localTarget].LastSuccess == nil {
		t.Error("the local copy is not recorded as having succeeded")
	}
	if state.Targets[githubTargetName].Ref != "abc123" {
		t.Error("the GitHub commit is not recorded, so the picker has nothing to show")
	}
	if state.Targets[driveTargetName].LastError == "" {
		t.Error("a failed target left no error behind; the failure is invisible")
	}

	lines := tailBackupLog(dir, 10)
	if len(lines) != 1 || !strings.Contains(lines[0], "drive FAILED") {
		t.Errorf("the log does not name the failed target: %v", lines)
	}
}

// Local dated folders are pruned automatically because they are redundant with
// the off-site copies. Today's is never pruned, whatever the arithmetic says.
func TestPruneDatedFolders(t *testing.T) {
	base := t.TempDir()
	today := time.Now().Format("2006-01-02")
	old := time.Now().AddDate(0, 0, -100).Format("2006-01-02")
	recent := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	for _, name := range []string{today, old, recent, "not-a-date"} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	pruned := pruneDatedFolders(base, 90, today)
	if len(pruned) != 1 || pruned[0] != old {
		t.Errorf("pruned %v, want only %s", pruned, old)
	}
	for _, name := range []string{today, recent, "not-a-date"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Errorf("%s was deleted and should not have been: %v", name, err)
		}
	}
}

func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// A target that has never succeeded is stale for a different reason than one
// whose last success is old, and the sentence has to say which.
//
// The bug this pins: the local archive always succeeds, so the moment an
// off-site target was misconfigured the age came from local (zero hours) while
// the staleness came from the target that had never run -- and every surface,
// including every student's sign-in, read "Backups have not run in 0 hours."
// The wording rule already existed for the case where *nothing* had succeeded;
// this is the mixed case, which is the one a site actually hits.
func TestStalenessTellsNeverApartFromOld(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()

	// GitHub enabled and never successful; the local archive written seconds
	// ago. Settings are restored by the cleanup so the suite is unaffected.
	before, err := db.loadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveSettings(ctx, admin, SettingsInput{
		GitHubEnabled: boolPtr(true),
		GitHubRepo:    strPtr("someone/backups"),
		GitHubToken:   strPtr("ghp_test"),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.SaveSettings(ctx, admin, SettingsInput{
			GitHubEnabled: boolPtr(before.GitHubEnabled),
			GitHubRepo:    strPtr(before.GitHubRepo),
		})
	})

	now := time.Now()
	writeBackupState(db.BackupDir, backupState{
		Targets:   map[string]targetState{localTarget: {LastSuccess: &now}},
		LastRunAt: &now,
	})

	status, err := db.BackupStatus(ctx, admin)
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	if !status.Stale {
		t.Fatal("a target that has never succeeded is not reported as stale")
	}
	if containsSubstring(status.Warnings, "0 hours") {
		t.Errorf("the warning counts an age that is not the reason: %v", status.Warnings)
	}
	if !containsSubstring(status.Warnings, "never succeeded") {
		t.Errorf("the warning does not say the github backup has never succeeded: %v", status.Warnings)
	}

	// Both sign-in audiences inherit the same sentence, so neither is told the
	// nonsense number either.
	invalidateBackupWarning()
	for _, isAdmin := range []bool{false, true} {
		w := db.backupWarningFor(ctx, isAdmin)
		if w == nil {
			t.Fatalf("no sign-in warning for isAdmin=%v", isAdmin)
		}
		if strings.Contains(w.Message, "0 hours") {
			t.Errorf("sign-in warning (isAdmin=%v) says %q", isAdmin, w.Message)
		}
	}
}

// The wording itself, without a database: the three shapes it has to tell
// apart, and the both-at-once case that must not drop either half.
func TestStaleSentenceWording(t *testing.T) {
	old := 120.0
	fresh := 0.0
	cases := []struct {
		name     string
		age      *float64
		byAge    bool
		never    []string
		contains []string
		absent   []string
	}{
		{"nothing has ever run", nil, false, []string{"local"},
			[]string{"never run on this machine"}, []string{"0 hours"}},
		{"one target has never run", &fresh, false, []string{"github"},
			[]string{"github", "never succeeded"}, []string{"0 hours", "not run in"}},
		{"two targets have never run", &fresh, false, []string{"drive", "github"},
			[]string{"drive and github", "never succeeded"}, []string{"0 hours"}},
		{"old but all succeeding", &old, true, nil,
			[]string{"5 days"}, []string{"never"}},
		{"old and one never ran", &old, true, []string{"github"},
			[]string{"5 days", "github", "never succeeded"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := staleSentence(c.age, c.byAge, c.never)
			for _, want := range c.contains {
				if !strings.Contains(got, want) {
					t.Errorf("%q does not contain %q", got, want)
				}
			}
			for _, no := range c.absent {
				if strings.Contains(got, no) {
					t.Errorf("%q should not contain %q", got, no)
				}
			}
		})
	}
}

// fakeTarget pushes or fails on command, so both halves of "one target broke"
// can be arranged without a Google account or a GitHub token.
type fakeTarget struct {
	name string
	err  error
}

func (f fakeTarget) Name() string { return f.name }
func (f fakeTarget) Push(context.Context, pushRequest) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "pushed-by-" + f.name, nil
}
func (f fakeTarget) Versions(context.Context) ([]BackupVersion, error)    { return nil, nil }
func (f fakeTarget) Fetch(context.Context, string) (io.ReadCloser, error) { return nil, nil }
func (f fakeTarget) Test(context.Context) error                           { return nil }

// Both targets on, one broken (CLAUDE.md §13, Phase C). The point of two targets is that one being blocked leaves the other
// working, so the broken one must not stop the good one, and the screen must
// say which one failed rather than that "the backup" did. Drive goes first
// and fails, so a loop that stopped at the first error would never reach
// GitHub.
func TestOneBrokenTargetLeavesTheOtherWorking(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	db.BackupDir = t.TempDir()

	restore := withTestSettings(t, db, admin, SettingsInput{
		DriveEnabled:  boolPtr(true),
		DriveRemote:   strPtr("gdrive"),
		GitHubEnabled: boolPtr(true),
		GitHubRepo:    strPtr("example/stockroom-backup"),
		GitHubToken:   strPtr("github_pat_FAKEFORTEST"),
	})
	defer restore()

	results := pushEach(ctx, []BackupTarget{
		fakeTarget{name: driveTargetName, err: errors.New("drive: dial tcp: i/o timeout")},
		fakeTarget{name: githubTargetName},
	}, pushRequest{})
	if len(results) != 2 {
		t.Fatalf("pushEach returned %d results for two targets: %+v", len(results), results)
	}
	if results[0].OK || !results[1].OK {
		t.Fatalf("results = %+v; want drive failed and github succeeded", results)
	}

	db.recordBackupRun(db.BackupDir, BackupResult{RanAt: time.Now(), Source: BackupSourceManual, Targets: results}, nil)
	status, err := db.BackupStatus(ctx, admin)
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	for _, row := range status.Targets {
		switch row.Target {
		case driveTargetName:
			if row.LastError == "" {
				t.Error("the Drive row carries no error after a failed push")
			}
		case githubTargetName:
			if row.LastSuccess == nil || row.LastError != "" {
				t.Errorf("the GitHub row = %+v; want a success and no error", row)
			}
		}
	}
	if !containsSubstring(status.Warnings, "drive backup failed") {
		t.Errorf("the warnings do not name the target that failed: %v", status.Warnings)
	}
	if containsSubstring(status.Warnings, "github backup failed") {
		t.Errorf("the warnings blame the target that worked: %v", status.Warnings)
	}
}
