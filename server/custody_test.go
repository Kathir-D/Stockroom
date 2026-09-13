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

func TestScanRoute(t *testing.T) {
	h, d := testDeps(t)
	_, sn := seedUser(t, d, false, "scan-route-password")
	token := login(t, h, sn, "scan-route-password")

	available, availableSerial := seedAsset(t, d, stockroom.StatusAvailable)
	code, body := call(t, h, http.MethodPost, "/scan", token, map[string]string{"serial": availableSerial})
	if code != http.StatusOK {
		t.Fatalf("scan available = %d %v", code, body)
	}
	if body["action"] != "detail" || body["checkable"] != true {
		t.Errorf("scan available: action %v checkable %v", body["action"], body["checkable"])
	}
	asset, ok := body["asset"].(map[string]any)
	if !ok || asset["id"] != available {
		t.Fatalf("scan payload has no asset: %v", body)
	}
	// The scan branch answers with the detail payload, category path and all,
	// so the frontend opens one dialog for both paths.
	if _, ok := asset["category_path"]; !ok {
		t.Errorf("scan payload is missing category_path: %v", asset)
	}

	_, unavailableSerial := seedAsset(t, d, stockroom.StatusUnavailable)
	_, body = call(t, h, http.MethodPost, "/scan", token, map[string]string{"serial": unavailableSerial})
	if body["action"] != "detail" || body["checkable"] != false {
		t.Errorf("scan unavailable: action %v checkable %v", body["action"], body["checkable"])
	}

	holderID, _ := seedUser(t, d, false, "holder-password")
	out, outSerial := seedAsset(t, d, stockroom.StatusAvailable)
	checkOut(t, d, out, holderID, time.Now().Add(24*time.Hour))
	code, body = call(t, h, http.MethodPost, "/scan", token, map[string]string{"serial": outSerial})
	if code != http.StatusOK || body["action"] != "checked_in" {
		t.Fatalf("scan checked-out = %d %v, want 200 checked_in", code, body)
	}
	if returned, ok := body["returned_from"].(map[string]any); !ok || returned["custodian_id"] != holderID {
		t.Errorf("returned_from = %v, want the holder", body["returned_from"])
	}

	if code, _ := call(t, h, http.MethodPost, "/scan", token, map[string]string{"serial": "NOPE-000"}); code != http.StatusNotFound {
		t.Errorf("unknown serial = %d, want 404", code)
	}
	if code, _ := call(t, h, http.MethodPost, "/scan", token, map[string]string{"serial": ""}); code != http.StatusBadRequest {
		t.Errorf("blank serial = %d, want 400", code)
	}
	// The mux answers 405 in plain text, before any session middleware.
	if rec := do(h, http.MethodGet, "/scan"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /scan = %d, want 405", rec.Code)
	}
	if code, _ := call(t, h, http.MethodPost, "/scan", "", map[string]string{"serial": availableSerial}); code != http.StatusUnauthorized {
		t.Errorf("scan with no session = %d, want 401", code)
	}
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

func TestCheckoutRouteOverdueBlock(t *testing.T) {
	h, d := testDeps(t)
	userID, sn := seedUser(t, d, false, "overdue-password")
	token := login(t, h, sn, "overdue-password")
	_, adminSN := seedUser(t, d, true, "admin-password")
	adminToken := login(t, h, adminSN, "admin-password")

	late, _ := seedAsset(t, d, stockroom.StatusAvailable)
	checkOut(t, d, late, userID, time.Now().Add(-48*time.Hour))
	wanted, _ := seedAsset(t, d, stockroom.StatusAvailable)

	code, body := call(t, h, http.MethodPost, "/checkout", token, map[string]any{
		"asset_ids": []string{wanted}, "due_at": dueIn(3),
	})
	if code != http.StatusConflict {
		t.Fatalf("overdue checkout = %d %v, want 409", code, body)
	}

	// The override is the admin's, and only with the flag set.
	code, _ = call(t, h, http.MethodPost, "/checkout", token, map[string]any{
		"asset_ids": []string{wanted}, "due_at": dueIn(3), "override_overdue": true,
	})
	if code != http.StatusForbidden {
		t.Errorf("non-admin override = %d, want 403", code)
	}
	code, body = call(t, h, http.MethodPost, "/checkout", adminToken, map[string]any{
		"custodian_id": userID, "asset_ids": []string{wanted}, "due_at": dueIn(3), "override_overdue": true,
	})
	if code != http.StatusOK {
		t.Errorf("admin override = %d %v, want 200", code, body)
	}
}

func TestCheckInRoute(t *testing.T) {
	h, d := testDeps(t)
	holderID, _ := seedUser(t, d, false, "holder-password")
	_, sn := seedUser(t, d, false, "returner-password")
	token := login(t, h, sn, "returner-password")

	withNote, _ := seedAsset(t, d, stockroom.StatusAvailable)
	checkOut(t, d, withNote, holderID, time.Now().Add(24*time.Hour))
	code, body := call(t, h, http.MethodPost, "/assets/"+withNote+"/checkin", token,
		map[string]any{"note": "lens cap missing"})
	if code != http.StatusOK {
		t.Fatalf("check in = %d %v", code, body)
	}
	returned, ok := body["returned_from"].(map[string]any)
	if !ok || returned["custodian_id"] != holderID {
		t.Errorf("returned_from = %v", body["returned_from"])
	}
	if asset, _ := body["asset"].(map[string]any); asset["status"] != string(stockroom.StatusAvailable) {
		t.Errorf("asset after check-in = %v", body["asset"])
	}

	// A note is optional, and so is the body that would carry it.
	noBody, _ := seedAsset(t, d, stockroom.StatusAvailable)
	checkOut(t, d, noBody, holderID, time.Now().Add(24*time.Hour))
	if code, body := call(t, h, http.MethodPost, "/assets/"+noBody+"/checkin", token, nil); code != http.StatusOK {
		t.Fatalf("check in with no body = %d %v", code, body)
	}

	if code, _ := call(t, h, http.MethodPost, "/assets/"+noBody+"/checkin", token, nil); code != http.StatusConflict {
		t.Errorf("second check in = %d, want 409", code)
	}
	if code, _ := call(t, h, http.MethodPost,
		"/assets/00000000-0000-0000-0000-0000000009ff/checkin", token, nil); code != http.StatusNotFound {
		t.Errorf("unknown asset = %d, want 404", code)
	}
	if code, _ := call(t, h, http.MethodPost, "/assets/not-a-uuid/checkin", token, nil); code != http.StatusBadRequest {
		t.Errorf("malformed id = %d, want 400", code)
	}
}

func TestCustodyListRoutes(t *testing.T) {
	h, d := testDeps(t)
	userID, sn := seedUser(t, d, false, "list-password")
	token := login(t, h, sn, "list-password")
	_, adminSN := seedUser(t, d, true, "admin-password")
	adminToken := login(t, h, adminSN, "admin-password")

	late, _ := seedAsset(t, d, stockroom.StatusAvailable)
	checkOut(t, d, late, userID, time.Now().Add(-72*time.Hour))

	for _, path := range []string{"/custody/active", "/custody/overdue"} {
		if code, _ := call(t, h, http.MethodGet, path, token, nil); code != http.StatusForbidden {
			t.Errorf("GET %s as a non-admin = %d, want 403", path, code)
		}
		code, body := call(t, h, http.MethodGet, path, adminToken, nil)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %v", path, code, body)
		}
		list, ok := body["_list"].([]any)
		if !ok {
			t.Fatalf("GET %s did not return a list: %v", path, body)
		}
		found := false
		for _, item := range list {
			row, _ := item.(map[string]any)
			if row["asset_id"] == late {
				found = true
				if row["custodian_name"] != "HTTP Test" {
					t.Errorf("custodian_name = %v, want a resolved name", row["custodian_name"])
				}
				if days, _ := row["days_overdue"].(float64); days < 3 {
					t.Errorf("days_overdue = %v, want at least 3", row["days_overdue"])
				}
			}
		}
		if !found {
			t.Errorf("GET %s is missing the overdue item", path)
		}
	}
}

func TestHistoryRoutes(t *testing.T) {
	h, d := testDeps(t)
	userID, sn := seedUser(t, d, false, "history-password")
	token := login(t, h, sn, "history-password")
	_, otherSN := seedUser(t, d, false, "other-password")
	otherToken := login(t, h, otherSN, "other-password")
	_, adminSN := seedUser(t, d, true, "admin-password")
	adminToken := login(t, h, adminSN, "admin-password")

	asset, _ := seedAsset(t, d, stockroom.StatusAvailable)
	checkOut(t, d, asset, userID, time.Now().Add(24*time.Hour))

	// The asset trail is admin-only even though /assets/{id} shows the
	// current holder to everyone.
	if code, _ := call(t, h, http.MethodGet, "/assets/"+asset+"/history", token, nil); code != http.StatusForbidden {
		t.Errorf("asset history as a non-admin = %d, want 403", code)
	}
	code, body := call(t, h, http.MethodGet, "/assets/"+asset+"/history", adminToken, nil)
	if code != http.StatusOK {
		t.Fatalf("asset history = %d %v", code, body)
	}
	if list, ok := body["_list"].([]any); !ok || len(list) != 1 {
		t.Errorf("asset history = %v, want one record", body)
	}
	if code, _ := call(t, h, http.MethodGet,
		"/assets/00000000-0000-0000-0000-0000000009ff/history", adminToken, nil); code != http.StatusNotFound {
		t.Errorf("unknown asset history = %d, want 404", code)
	}

	// Own history is not admin-only; someone else's is.
	if code, _ := call(t, h, http.MethodGet, "/users/"+userID+"/history", token, nil); code != http.StatusOK {
		t.Errorf("own history = %d, want 200", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/users/"+userID+"/history", otherToken, nil); code != http.StatusForbidden {
		t.Errorf("another user's history = %d, want 403", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/users/"+userID+"/history", adminToken, nil); code != http.StatusOK {
		t.Errorf("admin reading a user's history = %d, want 200", code)
	}
	// The admin user routes still resolve: adding {id}/history must not have
	// shadowed GET /users/{id}.
	if code, _ := call(t, h, http.MethodGet, "/users/"+userID, adminToken, nil); code != http.StatusOK {
		t.Errorf("GET /users/{id} = %d, want 200", code)
	}
	if rec := do(h, http.MethodPost, "/users/"+userID+"/history"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /users/{id}/history = %d, want 405", rec.Code)
	}
}
