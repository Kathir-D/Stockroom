package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"testing"
	"time"

	"stockroom/internal/stockroom"
)

// The Phase 4 routes over HTTP. The rules themselves are pinned one layer
// down in internal/stockroom; what these check is the wire: which sentinel
// becomes which status, which routes exist, and that a payload survives the
// round trip in the shape the frontends will read.

// seedAsset inserts an asset with a unique serial and the given status, and
// removes it (with any custody rows hanging off it) when the test ends.
func seedAsset(t *testing.T, d deps, status stockroom.AssetStatus) (id, serial string) {
	t.Helper()
	ctx := context.Background()
	serial = fmt.Sprintf("HTTP-%09d", rand.IntN(1_000_000_000))
	err := d.db.Pool.QueryRow(ctx, `
		insert into assets (asset_tag, name, serial_number, status)
		values ('HTTP-' || substr(md5(random()::text), 1, 12), 'HTTP test asset', $1, $2)
		returning id`, serial, string(status)).Scan(&id)
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	// custody_events cascades from assets, so one delete clears both and the
	// profile cleanup in seedUser can then succeed.
	t.Cleanup(func() { _, _ = d.db.Pool.Exec(ctx, `delete from assets where id = $1`, id) })
	return id, serial
}

// checkOut puts an asset in someone's hands directly, so a test can start
// from "already out" without going through the endpoint under test.
func checkOut(t *testing.T, d deps, assetID, custodianID string, dueAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.db.Pool.Exec(ctx,
		`update assets set status = 'checked_out' where id = $1`, assetID); err != nil {
		t.Fatal(err)
	}
	_, err := d.db.Pool.Exec(ctx, `
		insert into custody_events (asset_id, custodian_id, checked_out_by, due_at)
		values ($1, $2, $2, $3)`, assetID, custodianID, dueAt)
	if err != nil {
		t.Fatalf("check out fixture: %v", err)
	}
}

func dueIn(days int) string {
	return time.Now().Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
}

func TestCheckoutRoute(t *testing.T) {
	h, d := testDeps(t)
	userID, sn := seedUser(t, d, false, "checkout-password")
	token := login(t, h, sn, "checkout-password")
	a1, _ := seedAsset(t, d, stockroom.StatusAvailable)
	a2, _ := seedAsset(t, d, stockroom.StatusAvailable)

	code, body := call(t, h, http.MethodPost, "/checkout", token, map[string]any{
		"asset_ids": []string{a1, a2},
		"due_at":    dueIn(3),
	})
	if code != http.StatusOK {
		t.Fatalf("checkout = %d %v", code, body)
	}
	if body["custodian_id"] != userID {
		t.Errorf("custodian_id = %v, want the signed-in user", body["custodian_id"])
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v, want 2", body["items"])
	}

	// The same cart again: both items are out now, so the whole thing is a
	// 409 and the message names them.
	code, body = call(t, h, http.MethodPost, "/checkout", token, map[string]any{
		"asset_ids": []string{a1, a2},
		"due_at":    dueIn(3),
	})
	if code != http.StatusConflict {
		t.Fatalf("re-checkout = %d %v, want 409", code, body)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Error("409 body carries no error message for the UI to show")
	}

	free, _ := seedAsset(t, d, stockroom.StatusAvailable)
	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"empty cart", map[string]any{"asset_ids": []string{}, "due_at": dueIn(3)}, http.StatusBadRequest},
		{"no due date", map[string]any{"asset_ids": []string{free}}, http.StatusBadRequest},
		{"due date past the cap", map[string]any{"asset_ids": []string{free}, "due_at": dueIn(30)}, http.StatusBadRequest},
		{"malformed asset id", map[string]any{"asset_ids": []string{"nope"}, "due_at": dueIn(3)}, http.StatusBadRequest},
		{"unknown asset", map[string]any{"asset_ids": []string{"00000000-0000-0000-0000-0000000009ff"}, "due_at": dueIn(3)}, http.StatusNotFound},
		{"unknown field", map[string]any{"asset_ids": []string{free}, "due_at": dueIn(3), "nope": 1}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := call(t, h, http.MethodPost, "/checkout", token, tc.body); code != tc.want {
				t.Errorf("= %d %v, want %d", code, body, tc.want)
			}
		})
	}

	// Checking out to someone else is an admin power; a non-admin asking is
	// 403, not a silently ignored field.
	otherID, _ := seedUser(t, d, false, "other-password")
	code, _ = call(t, h, http.MethodPost, "/checkout", token, map[string]any{
		"custodian_id": otherID,
		"asset_ids":    []string{free},
		"due_at":       dueIn(3),
	})
	if code != http.StatusForbidden {
		t.Errorf("non-admin picking a custodian = %d, want 403", code)
	}
}
