package main

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"testing"

	"stockroom/internal/stockroom"
)

// The kit routes over HTTP. As with the rest of this package, the rules are
// pinned one layer down; what these check is the wire -- which routes exist,
// which sentinel becomes which status, and that a kit reaches checkout as its
// asset ids rather than through a kit-shaped checkout of its own.

// seedKit creates an empty kit through the API and removes it afterwards.
func seedKit(t *testing.T, h http.Handler, token string) (id, name string) {
	t.Helper()
	name = fmt.Sprintf("HTTP kit %09d", rand.IntN(1_000_000_000))
	code, body := call(t, h, http.MethodPost, "/kits", token, map[string]any{"name": name})
	if code != http.StatusOK {
		t.Fatalf("POST /kits = %d %v", code, body)
	}
	id, _ = body["id"].(string)
	if id == "" {
		t.Fatalf("POST /kits returned no id: %v", body)
	}
	t.Cleanup(func() { call(t, h, http.MethodDelete, "/kits/"+id, token, nil) })
	return id, name
}

// Build a kit, check its units out as a cart, and return them with one press.
func TestKitRoutesRoundTrip(t *testing.T) {
	h, d := testDeps(t)
	_, adminSN := seedUser(t, d, true, "kit-admin-password")
	token := login(t, h, adminSN, "kit-admin-password")
	a1, _ := seedAsset(t, d, stockroom.StatusAvailable)
	a2, _ := seedAsset(t, d, stockroom.StatusAvailable)

	kitID, kitName := seedKit(t, h, token)

	for _, id := range []string{a1, a2} {
		code, body := call(t, h, http.MethodPost, "/kits/"+kitID+"/items", token, map[string]any{"asset_id": id})
		if code != http.StatusOK {
			t.Fatalf("add %s to kit = %d %v", id, code, body)
		}
	}

	code, body := call(t, h, http.MethodGet, "/kits/"+kitID, token, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /kits/{id} = %d %v", code, body)
	}
	if body["name"] != kitName || body["checkable"] != true {
		t.Fatalf("kit = %v, want the created name and checkable", body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v, want 2", body["items"])
	}

	// A kit goes out through the ordinary cart endpoint, expanded into its
	// asset ids: one checkout path means the 7-day cap and the overdue block
	// cannot differ between a kit and a handful of units.
	code, body = call(t, h, http.MethodPost, "/checkout", token, map[string]any{
		"asset_ids": []string{a1, a2},
		"due_at":    dueIn(3),
	})
	if code != http.StatusOK {
		t.Fatalf("checkout of a kit's units = %d %v", code, body)
	}

	code, body = call(t, h, http.MethodGet, "/kits/"+kitID, token, nil)
	if code != http.StatusOK || body["checkable"] != false || body["checked_out"] != float64(2) {
		t.Fatalf("kit while out = %d %v, want checkable false and 2 out", code, body)
	}

	code, body = call(t, h, http.MethodPost, "/kits/"+kitID+"/checkin", token, nil)
	if code != http.StatusOK {
		t.Fatalf("POST /kits/{id}/checkin = %d %v", code, body)
	}
	returned, _ := body["returned"].([]any)
	if len(returned) != 2 {
		t.Fatalf("returned = %v, want both units", body["returned"])
	}
	// The three buckets are always present, never null, so the frontend does
	// not have to guard each one.
	for _, key := range []string{"returned", "already_in", "failed"} {
		if _, ok := body[key].([]any); !ok {
			t.Errorf("%s = %v, want a list", key, body[key])
		}
	}

	code, body = call(t, h, http.MethodDelete, "/kits/"+kitID+"/items/"+a1, token, nil)
	if code != http.StatusOK {
		t.Fatalf("DELETE a kit item = %d %v", code, body)
	}
	if code, body := call(t, h, http.MethodDelete, "/kits/"+kitID+"/items/"+a1, token, nil); code != http.StatusNotFound {
		t.Errorf("removing a non-member = %d %v, want 404", code, body)
	}
}

// Every kit write answers 403 to a student, while both reads answer 200: a
// student has to be able to see a kit to put it in a cart (CLAUDE.md §7).
func TestKitWriteRoutesRefuseAStudent(t *testing.T) {
	h, d := testDeps(t)
	_, adminSN := seedUser(t, d, true, "kit-gate-admin")
	adminToken := login(t, h, adminSN, "kit-gate-admin")
	_, studentSN := seedUser(t, d, false, "kit-gate-student")
	studentToken := login(t, h, studentSN, "kit-gate-student")
	assetID, _ := seedAsset(t, d, stockroom.StatusAvailable)
	kitID, _ := seedKit(t, h, adminToken)

	writes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/kits", map[string]any{"name": "student kit"}},
		{http.MethodPut, "/kits/" + kitID, map[string]any{"name": "renamed"}},
		{http.MethodDelete, "/kits/" + kitID, nil},
		{http.MethodPost, "/kits/" + kitID + "/items", map[string]any{"asset_id": assetID}},
		{http.MethodDelete, "/kits/" + kitID + "/items/" + assetID, nil},
	}
	for _, w := range writes {
		if code, body := call(t, h, w.method, w.path, studentToken, w.body); code != http.StatusForbidden {
			t.Errorf("%s %s as a student = %d %v, want 403", w.method, w.path, code, body)
		}
	}

	for _, path := range []string{"/kits", "/kits/" + kitID} {
		if code, body := call(t, h, http.MethodGet, path, studentToken, nil); code != http.StatusOK {
			t.Errorf("GET %s as a student = %d %v, want 200", path, code, body)
		}
	}
	// And returning one is not a write in this sense: whoever carries the bag
	// back is often not whoever signed it out.
	if code, body := call(t, h, http.MethodPost, "/kits/"+kitID+"/checkin", studentToken, nil); code != http.StatusOK {
		t.Errorf("kit check-in as a student = %d %v, want 200", code, body)
	}
}
