package stockroom

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The activity log (ROADMAP §2.4): one append-only, timestamped timeline of
// everything that happens at the closet -- the camera's walk-ins and
// walk-outs, sign-ins and sign-outs, every barcode scan, every checkout and
// check-in, and every admin change. Admin -> Activity reads it, so that when
// an item goes missing without being checked out an admin can see who signed
// in, what was scanned, and who was in the room.
//
// Three rules shape this file:
//
//   - A row is written INSIDE the transaction of the action it records
//     (writeLog takes the transaction), so an action cannot commit without
//     its row and a row cannot exist for an action that rolled back. Actions
//     that write nothing to the database -- a sign-in, a scan that only
//     opened an item -- write their row on its own, and a sign-in whose row
//     cannot be written is refused.
//   - The table is append-only. The migration's triggers refuse UPDATE,
//     DELETE and TRUNCATE for every role; nothing here tries.
//   - Every row is readable on its own. The actor's name and the item's label
//     are copied into the row when it is written, and so is a one-line
//     summary, because the account or the item may be deleted later and the
//     log is exactly what somebody reads afterwards.

// Log categories: the timeline's type filter.
const (
	LogCloset    = "closet"
	LogAccount   = "account"
	LogScan      = "scan"
	LogEquipment = "equipment"
	LogAdmin     = "admin"
)

var logCategories = []string{LogCloset, LogAccount, LogScan, LogEquipment, LogAdmin}

// LogEntry is one row to write.
type LogEntry struct {
	Category string
	Action   string
	Summary  string
	// ActorID is the account that did it; blank for the camera and the system.
	ActorID string
	AssetID string
	// AssetLabel is filled from the asset's serial when AssetID is set and
	// this is blank.
	AssetLabel string
	VisitID    string
	ViaScanner bool
	Details    map[string]any
	// At overrides the server clock, for events whose time is known and not
	// "now": a walk-in the detector saw a few seconds ago, a session that went
	// idle at a known moment. Zero means now().
	At time.Time
}

// --- request context --------------------------------------------------------

type logCtxKey int

const (
	ctxScreen logCtxKey = iota
	ctxScan
	ctxKit
)

// WithScreen records which screen of the UI the request came from. The server
// sets it from the X-Stockroom-Screen header on every request, so a scan's log
// row can say where it was scanned without every endpoint taking a field.
func WithScreen(ctx context.Context, screen string) context.Context {
	screen = strings.TrimSpace(screen)
	if len(screen) > 40 {
		screen = screen[:40]
	}
	if screen == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxScreen, screen)
}

func screenFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxScreen).(string)
	return s
}

// withScan marks everything below as caused by a barcode scan of code, so a
// check-in that a scan triggers is logged as a scan and not as a click.
func withScan(ctx context.Context, code string) context.Context {
	return context.WithValue(ctx, ctxScan, code)
}

func scanFrom(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(ctxScan).(string)
	return s, ok
}

// fromScanner fills in the scan fields of e from ctx: whether it came from the
// scanner, the code read and the screen.
func (e *LogEntry) fromContext(ctx context.Context) {
	if code, ok := scanFrom(ctx); ok {
		e.ViaScanner = true
		if e.Details == nil {
			e.Details = map[string]any{}
		}
		if _, set := e.Details["code"]; !set {
			e.Details["code"] = code
		}
	}
	if screen := screenFrom(ctx); screen != "" {
		if e.Details == nil {
			e.Details = map[string]any{}
		}
		if _, set := e.Details["screen"]; !set {
			e.Details["screen"] = screen
		}
	}
}

// --- writing ----------------------------------------------------------------

// writeLog inserts one row using q, which should be the transaction of the
// action being recorded. The actor's name and the asset's label are resolved
// in the same statement, so they are what the database held at that moment.
func writeLog(ctx context.Context, q querier, e LogEntry) error {
	e.fromContext(ctx)
	if e.Category == "" {
		e.Category = LogAdmin
	}
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("log %s: %w", e.Action, err)
	}
	var at *time.Time
	if !e.At.IsZero() {
		t := e.At
		at = &t
	}
	_, err = q.Exec(ctx, `
		insert into activity_log
			(category, action, summary, actor_id, actor_name, asset_id, asset_label,
			 visit_id, via_scanner, details, created_at)
		values ($1, $2, $3, $4::uuid,
			(select coalesce(nullif(trim(coalesce(first_name, '') || ' ' || coalesce(last_name, '')), ''),
			                 nullif(full_name, ''), student_number)
			   from profiles where id = $4::uuid),
			$5::uuid,
			coalesce(nullif($6, ''), (select coalesce(serial_number, name) from assets where id = $5::uuid)),
			$7::uuid, $8, $9::jsonb, coalesce($10, now()))`,
		e.Category, e.Action, e.Summary, nullIfBlank(e.ActorID), nullIfBlank(e.AssetID), e.AssetLabel,
		nullIfBlank(e.VisitID), e.ViaScanner, string(raw), at)
	if err != nil {
		return mapPgError("write the activity log", err)
	}
	return nil
}

// logNow writes a row outside any transaction, for actions that change
// nothing else. A failure is returned; the caller decides whether the action
// may proceed without its row.
func (db *DB) logNow(ctx context.Context, e LogEntry) error {
	return writeLog(ctx, db.Pool, e)
}

// logBestEffort is for the few rows whose action has already happened and
// cannot be undone -- a sign-out, an idle timeout, a camera going offline.
// Refusing them would change nothing, so a failure is logged to the server's
// own log instead.
func (db *DB) logBestEffort(ctx context.Context, e LogEntry) {
	if err := writeLog(ctx, db.Pool, e); err != nil {
		log.Printf("activity log: could not record %s: %v", e.Action, err)
	}
}

// markLogged tells the asset-status trigger that this transaction records
// its own actions, so a status flip does not add a second row, and names the
// actor for any row the trigger does write.
func markLogged(ctx context.Context, tx pgx.Tx, actorID string) error {
	_, err := tx.Exec(ctx,
		`select set_config('stockroom.logged', 'on', true), set_config('stockroom.actor_id', $1, true)`,
		actorID)
	if err != nil {
		return fmt.Errorf("mark transaction as logged: %w", err)
	}
	return nil
}

// withLoggedTx runs fn in a transaction marked by markLogged and commits it.
func (db *DB) withLoggedTx(ctx context.Context, actorID, what string, fn func(tx pgx.Tx) error) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := markLogged(ctx, tx, actorID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

func nullIfBlank(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// actorLogID is the id to record for actor: blank for the command line, which
// has no account.
func actorLogID(a Actor) string {
	if a.trustedCLI {
		return ""
	}
	return a.ID
}

// --- reading ----------------------------------------------------------------

// ActivityEntry is one row of the timeline.
type ActivityEntry struct {
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	Category   string         `json:"category"`
	Action     string         `json:"action"`
	Summary    string         `json:"summary"`
	ActorID    *string        `json:"actor_id"`
	ActorName  *string        `json:"actor_name"`
	AssetID    *string        `json:"asset_id"`
	AssetLabel *string        `json:"asset_label"`
	ViaScanner bool           `json:"via_scanner"`
	Details    map[string]any `json:"details"`
	// Visit is set on the camera's walked-in and walked-out rows.
	Visit *ClosetVisit `json:"visit,omitempty"`
}

// ActivityFilter narrows the timeline. Every field is optional.
type ActivityFilter struct {
	From       *time.Time
	To         *time.Time
	PersonID   string
	AssetID    string
	Categories []string
	ScansOnly  bool
	Query      string
	// Before pages backwards: rows strictly older than this time.
	Before *time.Time
	Limit  int
}

// ActivityPage is one page of the timeline, newest first.
type ActivityPage struct {
	Entries []ActivityEntry `json:"entries"`
	// More is true when older rows match the same filter; ask again with
	// Before set to the last entry's time.
	More bool `json:"more"`
}

const maxActivityPage = 500

func (f ActivityFilter) where() (string, []any, error) {
	var conds []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.From != nil {
		conds = append(conds, "l.created_at >= "+arg(*f.From))
	}
	if f.To != nil {
		conds = append(conds, "l.created_at <= "+arg(*f.To))
	}
	if f.Before != nil {
		conds = append(conds, "l.created_at < "+arg(*f.Before))
	}
	if f.PersonID != "" {
		conds = append(conds, "l.actor_id = "+arg(f.PersonID)+"::uuid")
	}
	if f.AssetID != "" {
		conds = append(conds, "l.asset_id = "+arg(f.AssetID)+"::uuid")
	}
	if len(f.Categories) > 0 {
		for _, c := range f.Categories {
			if !slicesContains(logCategories, c) {
				return "", nil, fmt.Errorf("%w: unknown event type %q", ErrInvalid, c)
			}
		}
		conds = append(conds, "l.category = any("+arg(f.Categories)+")")
	}
	if f.ScansOnly {
		conds = append(conds, "l.via_scanner")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		p := arg("%" + q + "%")
		conds = append(conds, "(l.summary ilike "+p+" or l.actor_name ilike "+p+" or l.asset_label ilike "+p+" or l.details::text ilike "+p+")")
	}
	if len(conds) == 0 {
		return "true", args, nil
	}
	return strings.Join(conds, " and "), args, nil
}

// ListActivity is GET /admin/activity. Admin only: the log names every
// student's movements, and the API refuses everyone else rather than the UI
// hiding a tab.
func (db *DB) ListActivity(ctx context.Context, actor Actor, f ActivityFilter) (ActivityPage, error) {
	if err := RequireAdmin(actor); err != nil {
		return ActivityPage{}, err
	}
	limit := f.Limit
	if limit <= 0 || limit > maxActivityPage {
		limit = 200
	}
	where, args, err := f.where()
	if err != nil {
		return ActivityPage{}, err
	}
	args = append(args, limit+1)
	rows, err := db.Pool.Query(ctx, `
		select `+activityColumns+`
		from activity_log l
		left join closet_visits v on v.id = l.visit_id
		where `+where+`
		order by l.created_at desc, l.id desc
		limit $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return ActivityPage{}, mapPgError("list activity", err)
	}
	entries, err := scanActivityRows(rows)
	if err != nil {
		return ActivityPage{}, err
	}
	page := ActivityPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		page.More = true
	}
	return page, nil
}

const activityColumns = `l.id, l.created_at, l.category, l.action, l.summary, l.actor_id, l.actor_name,
	l.asset_id, l.asset_label, l.via_scanner, l.details, ` + visitColumnsNullable

func scanActivityRows(rows pgx.Rows) ([]ActivityEntry, error) {
	defer rows.Close()
	out := []ActivityEntry{}
	for rows.Next() {
		var e ActivityEntry
		var raw []byte
		var v nullableVisit
		dest := []any{&e.ID, &e.At, &e.Category, &e.Action, &e.Summary, &e.ActorID, &e.ActorName,
			&e.AssetID, &e.AssetLabel, &e.ViaScanner, &raw}
		dest = append(dest, v.dest()...)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Details)
		}
		if e.Details == nil {
			e.Details = map[string]any{}
		}
		e.Visit = v.visit()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	return out, nil
}

// ExportActivityCSV is GET /admin/activity.csv: the filtered range, oldest
// first, as a spreadsheet. The export is itself an admin action and is
// logged, because a copy of the timeline leaving the machine is something the
// next person to read the log should be able to see happened.
func (db *DB) ExportActivityCSV(ctx context.Context, actor Actor, f ActivityFilter, w io.Writer) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}
	where, args, err := f.where()
	if err != nil {
		return err
	}
	rows, err := db.Pool.Query(ctx, `
		select `+activityColumns+`
		from activity_log l
		left join closet_visits v on v.id = l.visit_id
		where `+where+`
		order by l.created_at, l.id`, args...)
	if err != nil {
		return mapPgError("export activity", err)
	}
	entries, err := scanActivityRows(rows)
	if err != nil {
		return err
	}
	if err := db.logNow(ctx, LogEntry{
		Category: LogAdmin, Action: "activity_exported", ActorID: actorLogID(actor),
		Summary: fmt.Sprintf("Exported %d activity rows as CSV", len(entries)),
		Details: map[string]any{"rows": len(entries), "from": f.From, "to": f.To},
	}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "type", "event", "summary", "person", "item", "via_scanner", "visit_started", "visit_ended", "details"})
	for _, e := range entries {
		var vs, ve string
		if e.Visit != nil {
			vs = e.Visit.StartedAt.Format(time.RFC3339)
			if e.Visit.EndedAt != nil {
				ve = e.Visit.EndedAt.Format(time.RFC3339)
			}
		}
		raw, _ := json.Marshal(e.Details)
		_ = cw.Write([]string{
			e.At.Format(time.RFC3339), e.Category, e.Action, e.Summary,
			deref(e.ActorName), deref(e.AssetLabel), fmt.Sprint(e.ViaScanner), vs, ve, string(raw),
		})
	}
	cw.Flush()
	return cw.Error()
}

// --- the unauthenticated scan ------------------------------------------------

// LogUnattendedScan records a barcode scanned with nobody signed in, or a scan
// the sign-in screen rejected before it reached a login (an item barcode read
// at the sign-in screen). ROADMAP §2.4 asks for every scan, "including scans
// with nobody signed in and scans that did nothing", and these are the ones
// no other endpoint sees.
//
// It is the one unauthenticated write besides the logins, so it takes only a
// short code and a fixed set of outcomes, and it is rate limited by the HTTP
// layer.
func (db *DB) LogUnattendedScan(ctx context.Context, code, result string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("%w: no code", ErrInvalid)
	}
	if len(code) > 64 {
		code = code[:64]
	}
	var summary string
	switch result {
	case "not_signed_in":
		summary = fmt.Sprintf("Scanned %s with nobody signed in", code)
	case "item_at_signin":
		summary = fmt.Sprintf("Scanned item barcode %s at the sign-in screen", code)
	case "invalid":
		summary = fmt.Sprintf("Scanned %s: not a student number or an item", code)
	default:
		return fmt.Errorf("%w: unknown scan result %q", ErrInvalid, result)
	}
	ctx = withScan(ctx, code)
	return db.logNow(ctx, LogEntry{
		Category: LogScan, Action: "scan_refused", Summary: summary,
		Details: map[string]any{"result": result},
	})
}
