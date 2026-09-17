package stockroom

import (
	"context"
	"encoding/json"
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
