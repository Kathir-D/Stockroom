package stockroom

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Failure visibility (docs/design/backup.md §E.7).
//
// A backup system fails silently by default: nothing goes wrong on the screen
// of the person using the app, and the morning it matters is the first anybody
// hears of it. Three things are therefore surfaced rather than logged and
// forgotten -- how long it has been since a successful run, whether the photo
// mirror is about to fill its disk, and whether there is a failsafe admin to
// sign in as if the database is lost.

// stateFile and logFile sit beside the dated folders, not inside one: they
// describe the series of runs rather than any single run.
const (
	stateFile   = ".last-success.json"
	logFile     = "backup.log"
	logMaxBytes = 5 << 20
	logTailMax  = 200
)

// backupState is what one machine remembers between runs, per target. "local"
// is a target here even though it is not a BackupTarget: a run that failed
// before it wrote anything is the most important failure to see, and calling
// it something other than a target would only mean a second shape to render.
type backupState struct {
	Targets       map[string]targetState `json:"targets"`
	LastRunAt     *time.Time             `json:"last_run_at"`
	LastRunSource string                 `json:"last_run_source"`
}

type targetState struct {
	LastSuccess *time.Time `json:"last_success"`
	Ref         string     `json:"ref"`
	Rows        int64      `json:"rows"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
}

// localTarget is the name the on-disk copy goes by in the state file and on
// the backup screen.
const localTarget = "local"

func readBackupState(baseDir string) backupState {
	s := backupState{Targets: map[string]targetState{}}
	raw, err := os.ReadFile(filepath.Join(baseDir, stateFile))
	if err != nil {
		return s
	}
	// A corrupt state file must not stop backups: the worst it can do is
	// forget when the last one succeeded, which makes the next run look stale
	// and run early. That is the safe direction to fail in.
	if err := json.Unmarshal(raw, &s); err != nil || s.Targets == nil {
		return backupState{Targets: map[string]targetState{}}
	}
	return s
}

func writeBackupState(baseDir string, s backupState) {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(baseDir, stateFile), raw, 0o644)
}

// recordBackupRun updates the state file and appends one line to backup.log.
// It never returns an error: a run that succeeded and then failed to write a
// log line has still backed the database up, and reporting the second failure
// as the first would be a lie about what happened.
func (db *DB) recordBackupRun(baseDir string, res BackupResult, runErr error) {
	if baseDir == "" || res.Skipped {
		return
	}
	defer invalidateBackupWarning()

	state := readBackupState(baseDir)
	now := time.Now()
	state.LastRunAt = &now
	state.LastRunSource = res.Source

	local := state.Targets[localTarget]
	if runErr != nil {
		local.LastError = runErr.Error()
		local.LastErrorAt = &now
	} else {
		at := res.RanAt
		local.LastSuccess = &at
		local.Ref = res.Archive
		local.Rows = res.Rows
		local.LastError = ""
		local.LastErrorAt = nil
	}
	state.Targets[localTarget] = local

	for _, t := range res.Targets {
		st := state.Targets[t.Target]
		if t.OK {
			at := res.RanAt
			st.LastSuccess = &at
			st.Ref = t.Ref
			st.Rows = res.Rows
			st.LastError = ""
			st.LastErrorAt = nil
		} else {
			st.LastError = t.Error
			st.LastErrorAt = &now
		}
		state.Targets[t.Target] = st
	}
	writeBackupState(baseDir, state)
	appendBackupLog(baseDir, backupLogLine(res, runErr))
}

func backupLogLine(res BackupResult, runErr error) string {
	when := time.Now().Format("2006-01-02 15:04:05")
	if runErr != nil {
		return fmt.Sprintf("%s  %-12s FAILED  %v", when, res.Source, runErr)
	}
	parts := []string{fmt.Sprintf("%d rows in %d tables", res.Rows, len(res.Tables))}
	if res.Encrypted {
		parts = append(parts, "encrypted")
	}
	for _, t := range res.Targets {
		if t.OK {
			parts = append(parts, t.Target+" ok")
		} else {
			parts = append(parts, t.Target+" FAILED: "+oneLine(t.Error))
		}
	}
	if res.Photos != nil {
		parts = append(parts, res.Photos.logSummary())
	}
	if len(res.Pruned) > 0 {
		parts = append(parts, fmt.Sprintf("pruned %d old folders", len(res.Pruned)))
	}
	return fmt.Sprintf("%s  %-12s ok      %s", when, res.Source, strings.Join(parts, " · "))
}

// oneLine flattens an error for the log, which is one run per line: a message
// carrying a newline would otherwise read as two runs, one of them nonsense.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// appendBackupLog adds a line, rotating the file once it passes 5 MB. One
// generation is kept: the log is a convenience for reading what happened
// recently, and the state file is what anything actually depends on.
func appendBackupLog(baseDir, line string) {
	path := filepath.Join(baseDir, logFile)
	if info, err := os.Stat(path); err == nil && info.Size() > logMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// tailBackupLog returns the last n lines, newest last, for the backup screen.
func tailBackupLog(baseDir string, n int) []string {
	raw, err := os.ReadFile(filepath.Join(baseDir, logFile))
	if err != nil {
		return []string{}
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

/* -------------------------------------------------------------- status ---- */

// BackupTargetStatus is one row of the per-target table on the backup screen.
type BackupTargetStatus struct {
	Target      string     `json:"target"`
	Enabled     bool       `json:"enabled"`
	LastSuccess *time.Time `json:"last_success"`
	AgeHours    *float64   `json:"age_hours"`
	Ref         string     `json:"ref"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
}

// BackupStatusResult is everything the backup screen renders and everything
// the staleness warning is computed from.
type BackupStatusResult struct {
	Configured    bool                 `json:"configured"`
	Dir           string               `json:"dir"`
	StaleHours    int                  `json:"stale_hours"`
	Stale         bool                 `json:"stale"`
	WorstAgeHours *float64             `json:"worst_age_hours"`
	Targets       []BackupTargetStatus `json:"targets"`
	LastRunAt     *time.Time           `json:"last_run_at"`
	LastRunSource string               `json:"last_run_source"`

	// FailsafeAdminConfigured is the §C.1 gap made visible. The failsafe is
	// best-effort by decision, so a site that never filled in .env restores a
	// wiped database into zero accounts and finds the admin panel -- the whole
	// documented restore route -- unreachable, at the one moment it is needed.
	// The check costs one comparison and turns a latent unrecoverability into
	// a visible setup defect.
	FailsafeAdminConfigured bool `json:"failsafe_admin_configured"`

	PhotoMirror *PhotoMirrorStatus `json:"photo_mirror"`

	// Warnings are the plain sentences the screen shows in a banner, already
	// worded. The UI decides how to render them, not what they say.
	Warnings []string `json:"warnings"`
	Log      []string `json:"log"`
	Schedule string   `json:"schedule"`
}

// failsafeConfigured is set by the server at start-up, because whether a
// failsafe admin exists is a fact about .env and the boot sequence rather than
// about the database: an account with is_admin could have been created any
// number of other ways.
var failsafeConfigured struct {
	mu sync.Mutex
	ok bool
}

// SetFailsafeAdminConfigured records what EnsureFailsafeAdmin returned at
// start-up, so BackupStatus can report it.
func SetFailsafeAdminConfigured(ok bool) {
	failsafeConfigured.mu.Lock()
	defer failsafeConfigured.mu.Unlock()
	failsafeConfigured.ok = ok
}

func failsafeAdminConfigured() bool {
	failsafeConfigured.mu.Lock()
	defer failsafeConfigured.mu.Unlock()
	return failsafeConfigured.ok
}

// BackupStatus is the backup screen's one read. Admin-only: it names folders,
// a schedule and a setup defect, none of which is a student's business and all
// of which the admin is the only person who can act on.
func (db *DB) BackupStatus(ctx context.Context, actor Actor) (BackupStatusResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return BackupStatusResult{}, err
	}
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return BackupStatusResult{}, err
	}
	out := BackupStatusResult{
		StaleHours:              settings.StaleHours,
		FailsafeAdminConfigured: failsafeAdminConfigured(),
		Warnings:                []string{},
		Targets:                 []BackupTargetStatus{},
		Log:                     []string{},
		Schedule:                fmt.Sprintf("%02d:00 daily", settings.ScheduleHour),
	}

	dir := db.BackupDir
	if dir == "" {
		dir = settings.BackupDir
	}
	out.Dir = dir
	out.Configured = dir != ""
	if !out.Configured {
		out.Warnings = append(out.Warnings, "No backup folder is set, so nothing is being backed up. Choose one under Settings.")
		if !out.FailsafeAdminConfigured {
			out.Warnings = append(out.Warnings, failsafeWarning)
		}
		return out, nil
	}

	state := readBackupState(dir)
	out.LastRunAt = state.LastRunAt
	out.LastRunSource = state.LastRunSource
	out.Log = tailBackupLog(dir, logTailMax)

	now := time.Now()
	for _, name := range []string{localTarget, driveTargetName, githubTargetName} {
		st := state.Targets[name]
		row := BackupTargetStatus{
			Target:      name,
			Enabled:     name == localTarget || targetEnabled(settings, name),
			LastSuccess: st.LastSuccess,
			Ref:         st.Ref,
			LastError:   st.LastError,
			LastErrorAt: st.LastErrorAt,
		}
		if st.LastSuccess != nil {
			age := now.Sub(*st.LastSuccess).Hours()
			row.AgeHours = &age
		}
		out.Targets = append(out.Targets, row)

		// Staleness is measured over the targets that are *supposed* to be
		// running. A disabled target has no age, and a target that has never
		// succeeded is as stale as it gets.
		if !row.Enabled {
			continue
		}
		if row.AgeHours == nil {
			out.Stale = true
			continue
		}
		if out.WorstAgeHours == nil || *row.AgeHours > *out.WorstAgeHours {
			age := *row.AgeHours
			out.WorstAgeHours = &age
		}
		if *row.AgeHours > float64(settings.StaleHours) {
			out.Stale = true
		}
	}

	if out.Stale {
		out.Warnings = append(out.Warnings, staleSentence(out.WorstAgeHours))
	}
	for _, t := range out.Targets {
		if t.Enabled && t.LastError != "" {
			out.Warnings = append(out.Warnings, fmt.Sprintf("The last %s backup failed: %s", t.Target, oneLine(t.LastError)))
		}
	}
	if !out.FailsafeAdminConfigured {
		out.Warnings = append(out.Warnings, failsafeWarning)
	}

	if photos, err := db.PhotoMirrorStatus(ctx, settings); err == nil && photos != nil {
		out.PhotoMirror = photos
		out.Warnings = append(out.Warnings, photos.Warnings...)
	}
	return out, nil
}

const failsafeWarning = "No failsafe admin is configured. If the database is lost you will not be able to sign in to restore it. Set ADMIN_STUDENT_NUMBER and ADMIN_PASSWORD in .env and restart the server."

// staleSentence is the one place the staleness wording lives, so the banner,
// the admin's sign-in warning and a student's all count the same days.
//
// A backup that has never run gets its own sentence rather than an age,
// because "has not run in 0 hours" is the kind of message that makes a person
// stop reading warnings.
func staleSentence(worstAgeHours *float64) string {
	if worstAgeHours == nil {
		return "Backups have never run on this machine."
	}
	return "Backups have not run in " + staleAge(worstAgeHours) + "."
}

// staleAge is a bare duration -- "18 hours", "5 days" -- so callers can put it
// in whichever sentence they are building.
func staleAge(worstAgeHours *float64) string {
	if worstAgeHours == nil {
		return "a while"
	}
	hours := *worstAgeHours
	if hours < 48 {
		return fmt.Sprintf("%d hours", int(hours))
	}
	return fmt.Sprintf("%d days", int(hours/24))
}

/* ------------------------------------------------------------ warnings ---- */

// BackupWarning rides on LoginResult and MeResult beside HasOverdue, which
// already had exactly this shape. Both audiences see the same fact and a
// different sentence: an admin is told where to go, a student is told who to
// tell.
type BackupWarning struct {
	Message string `json:"message"`
	// Admins are the people a student should mention it to, by *name only*.
	// Never a student number: a number is a working scan login (CLAUDE.md §7),
	// so a warning carrying one would hand every student an admin credential.
	Admins []string `json:"admins"`
}

// backupWarningTTL bounds how often the warning is recomputed. Sign-in happens
// on every scan of a card, and the computation reads a file and asks the
// database for the admin list; a minute of staleness in a warning about a
// backup that is days stale changes nothing.
const backupWarningTTL = 60 * time.Second

var backupWarningCache struct {
	mu   sync.Mutex
	at   time.Time
	val  *BackupWarning
	adm  *BackupWarning
	held bool
}

func invalidateBackupWarning() {
	backupWarningCache.mu.Lock()
	defer backupWarningCache.mu.Unlock()
	backupWarningCache.held = false
}

// backupWarningFor returns the warning to show this actor, or nil.
//
// It never returns an error. A warning is a courtesy on top of signing in, and
// a backup folder that cannot be read must not stop a student borrowing a
// camera.
func (db *DB) backupWarningFor(ctx context.Context, isAdmin bool) *BackupWarning {
	backupWarningCache.mu.Lock()
	fresh := backupWarningCache.held && time.Since(backupWarningCache.at) < backupWarningTTL
	if fresh {
		w, a := backupWarningCache.val, backupWarningCache.adm
		backupWarningCache.mu.Unlock()
		if isAdmin {
			return a
		}
		return w
	}
	backupWarningCache.mu.Unlock()

	student, admin := db.computeBackupWarnings(ctx)

	backupWarningCache.mu.Lock()
	backupWarningCache.val, backupWarningCache.adm = student, admin
	backupWarningCache.at, backupWarningCache.held = time.Now(), true
	backupWarningCache.mu.Unlock()

	if isAdmin {
		return admin
	}
	return student
}

// computeBackupWarnings builds both sentences at once, because they come from
// one reading of the same state and differ only in who is being addressed.
func (db *DB) computeBackupWarnings(ctx context.Context) (student, admin *BackupWarning) {
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return nil, nil
	}
	dir := db.BackupDir
	if dir == "" {
		dir = settings.BackupDir
	}
	if dir == "" {
		// Nothing is configured. That is an admin's job to fix and not
		// something a student can act on, so only the admin is told.
		return nil, &BackupWarning{Message: "Backups are not set up yet. Open Admin → Settings and choose a backup folder."}
	}

	state := readBackupState(dir)
	now := time.Now()
	var worst *float64
	stale := false
	for _, name := range []string{localTarget, driveTargetName, githubTargetName} {
		if name != localTarget && !targetEnabled(settings, name) {
			continue
		}
		st := state.Targets[name]
		if st.LastSuccess == nil {
			// Never having run is only stale once there has been time to run:
			// a machine set up an hour ago is not behind on anything.
			if state.LastRunAt == nil {
				continue
			}
			stale = true
			continue
		}
		age := now.Sub(*st.LastSuccess).Hours()
		if worst == nil || age > *worst {
			worst = &age
		}
		if age > float64(settings.StaleHours) {
			stale = true
		}
	}
	if !stale {
		return nil, nil
	}

	sentence := staleSentence(worst)
	admins := db.adminNames(ctx)
	student = &BackupWarning{Message: sentence, Admins: admins}
	if len(admins) > 0 {
		student.Message = sentence + " Please tell " + joinNames(admins) + "."
	}
	return student, &BackupWarning{Message: sentence + " Open Admin → Backup.", Admins: admins}
}

// adminNames lists the admins by name, for the sentence a student is shown.
// Names only, and the query does not even select the student number, so the
// rule cannot be broken by a later edit to the formatting.
func (db *DB) adminNames(ctx context.Context) []string {
	rows, err := db.Pool.Query(ctx, `
		select coalesce(nullif(trim(coalesce(first_name,'') || ' ' || coalesce(last_name,'')), ''), full_name)
		from profiles where is_admin = true
		order by 1
		limit 5`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var name *string
		if err := rows.Scan(&name); err != nil {
			return names
		}
		if n := strings.TrimSpace(deref(name)); n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
	}
}
