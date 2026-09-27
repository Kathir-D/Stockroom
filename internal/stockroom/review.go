package stockroom

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// What an admin has to look at (ROADMAP §3.2, §3.3): returns that carry a
// damage note or that no scan backs up, and items that were lost while out.
// None of it blocks anyone. It is a list, a badge and a sign-in line, so the
// closet keeps working while somebody catches up with it.

// ListNeedsReview is every return an admin has not cleared yet, newest first.
// Admin only.
func (db *DB) ListNeedsReview(ctx context.Context, actor Actor) ([]CustodyRecord, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	return listCustody(ctx, db.Pool, `ce.reviewed_at is null and ce.review_reasons <> '{}'`,
		`ce.checked_in_at desc nulls last`)
}

// ResolveReview clears one return from the list. The reasons stay on the
// row, so its history still says why it was listed.
func (db *DB) ResolveReview(ctx context.Context, actor Actor, custodyEventID string) (CustodyRecord, error) {
	if err := RequireAdmin(actor); err != nil {
		return CustodyRecord{}, err
	}
	err := db.withLoggedTx(ctx, actorLogID(actor), "clear review", func(tx pgx.Tx) error {
		var assetID string
		var reasons []string
		err := tx.QueryRow(ctx, `
			update custody_events set reviewed_at = now(), reviewed_by = $2
			 where id = $1 and reviewed_at is null and review_reasons <> '{}'
			returning asset_id, review_reasons`, custodyEventID, nullableID(actor)).Scan(&assetID, &reasons)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: nothing to review on that return", ErrNotFound)
		}
		if err != nil {
			return mapPgError("clear review", err)
		}
		return writeLog(ctx, tx, LogEntry{
			Category: LogAdmin, Action: "review_cleared", ActorID: actorLogID(actor), AssetID: assetID,
			Summary: "Reviewed a return (" + strings.Join(reviewWords(reasons), ", ") + ")",
			Details: map[string]any{"custody_event_id": custodyEventID, "review_reasons": reasons},
		})
	})
	if err != nil {
		return CustodyRecord{}, err
	}
	records, err := listCustody(ctx, db.Pool, `ce.id = $1`, `ce.checked_out_at desc`, custodyEventID)
	if err != nil {
		return CustodyRecord{}, err
	}
	if len(records) == 0 {
		return CustodyRecord{}, fmt.Errorf("%w: no such custody event", ErrNotFound)
	}
	return records[0], nil
}

// nullableID is the actor's id for a uuid column, or nil for the CLI actor,
// whose id is not a uuid.
func nullableID(a Actor) any {
	if a.trustedCLI {
		return nil
	}
	return a.ID
}

func reviewWords(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		switch r {
		case ReviewDamage:
			out = append(out, "damage reported")
		case ReviewNotScanned:
			out = append(out, "returned without a scan")
		default:
			out = append(out, r)
		}
	}
	return out
}

// MarkAssetLost closes an item's open loan without the item (ROADMAP §3.2).
// Before this an admin could only record a return that never happened and
// then mark the item unavailable, which left the history claiming it came
// back. The loan closes with outcome "lost" and the note, the item becomes
// unavailable, and the borrower is no longer overdue on it: an admin has
// dealt with it, and anything more (a fee, a talk) happens outside the app.
func (db *DB) MarkAssetLost(ctx context.Context, actor Actor, assetID string, note *string) (AssetDetail, error) {
	if err := RequireAdmin(actor); err != nil {
		return AssetDetail{}, err
	}
	note = trimOptional(note)
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return AssetDetail{}, fmt.Errorf("mark lost: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := markLogged(ctx, tx, actorLogID(actor)); err != nil {
		return AssetDetail{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select true from assets where id = $1 for update`, assetID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AssetDetail{}, fmt.Errorf("%w: no such asset", ErrNotFound)
		}
		return AssetDetail{}, mapPgError("mark lost", err)
	}
	held, err := currentCustody(ctx, tx, assetID, actor)
	if err != nil {
		return AssetDetail{}, err
	}
	if held == nil {
		return AssetDetail{}, fmt.Errorf("%w: item is not checked out; mark it unavailable instead", ErrConflict)
	}
	if _, err := tx.Exec(ctx, `
		update custody_events
		   set checked_in_at = now(), checked_in_by = $2, condition_in = $3,
		       returned_via = 'lost', outcome = 'lost'
		 where id = $1`, held.CustodyEventID, nullableID(actor), note); err != nil {
		return AssetDetail{}, mapPgError("mark lost", err)
	}
	if _, err := tx.Exec(ctx, `update assets set status = 'unavailable' where id = $1`, assetID); err != nil {
		return AssetDetail{}, mapPgError("mark lost", err)
	}
	summary := fmt.Sprintf("Marked %s lost, last held by %s", assetLabel(ctx, tx, assetID), held.CustodianName)
	if note != nil {
		summary += ": " + *note
	}
	if err := writeLog(ctx, tx, LogEntry{
		Category: LogEquipment, Action: "marked_lost", ActorID: actorLogID(actor), AssetID: assetID, Summary: summary,
		Details: map[string]any{"custody_event_id": held.CustodyEventID, "custodian_id": held.CustodianID,
			"custodian_name": held.CustodianName},
	}); err != nil {
		return AssetDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AssetDetail{}, fmt.Errorf("mark lost: %w", err)
	}
	return db.GetAsset(ctx, actor, assetID)
}

// adminNoticeFor is the sign-in line that tells an admin what is waiting:
// items overdue and returns to review (ROADMAP §3.3). An overdue student may
// never sign in again, so without this the Overdue tab was the only place
// anybody would find out. Nil for a student, and when there is nothing.
func (db *DB) adminNoticeFor(ctx context.Context, isAdmin bool) *string {
	if !isAdmin {
		return nil
	}
	var overdue, review int
	if err := db.Pool.QueryRow(ctx, `
		select (select count(*) from overdue_custody),
		       (select count(*) from custody_events where reviewed_at is null and review_reasons <> '{}')`).
		Scan(&overdue, &review); err != nil {
		return nil
	}
	var parts []string
	if overdue > 0 {
		parts = append(parts, plural(overdue, "item is", "items are")+" overdue")
	}
	if review > 0 {
		parts = append(parts, plural(review, "return needs", "returns need")+" to be checked")
	}
	if len(parts) == 0 {
		return nil
	}
	msg := strings.Join(parts, ", and ") + ". See Admin, Needs attention."
	return &msg
}
