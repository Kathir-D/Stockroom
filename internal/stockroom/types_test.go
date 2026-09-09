package stockroom

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The password hash must never reach a client. It is tagged `json:"-"`; this
// pins that down because Profile is returned by /me and the user endpoints.
func TestProfileJSONNeverLeaksPasswordHash(t *testing.T) {
	hash := "$2a$10$notarealhashbutlooksplausible"
	email := "student@school.edu"
	p := Profile{
		ID:           "00000000-0000-0000-0000-000000000020",
		Email:        &email,
		PasswordHash: &hash,
		Role:         RoleMember,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), hash) || strings.Contains(string(b), "password_hash") {
		t.Fatalf("Profile JSON leaks the password hash: %s", b)
	}

	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := round["password_hash"]; ok {
		t.Error("password_hash present in the serialised profile")
	}
	// The fields the frontend actually reads must be there, including the v1
	// columns that are still null before the Phase 1 migration.
	for _, k := range []string{"id", "email", "student_number", "first_name", "last_name", "is_admin", "photo_path"} {
		if _, ok := round[k]; !ok {
			t.Errorf("profile JSON is missing %q", k)
		}
	}
	if round["is_admin"] != false {
		t.Errorf("is_admin = %v, want false by default", round["is_admin"])
	}
}

// Nullable columns are pointers so "absent" and "zero" stay distinguishable in
// JSON; a plain string would turn a null description into "".
func TestNullableFieldsMarshalAsNull(t *testing.T) {
	b, err := json.Marshal(Asset{ID: "a", AssetTag: "CAM-001", Name: "Sony A7S III", Status: StatusAvailable})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"description", "serial_number", "photo_path", "category_id", "purchase_price"} {
		v, ok := got[k]
		if !ok {
			t.Errorf("asset JSON is missing %q", k)
			continue
		}
		if v != nil {
			t.Errorf("%q = %v, want null when unset", k, v)
		}
	}
	if got["status"] != "available" {
		t.Errorf("status = %v, want %q", got["status"], "available")
	}
}

// ActiveCustody embeds CustodyEvent; the embedded fields must be flattened
// into the same JSON object rather than nested under a key.
func TestActiveCustodyFlattensEmbeddedEvent(t *testing.T) {
	b, err := json.Marshal(ActiveCustody{
		CustodyEvent: CustodyEvent{ID: "e1", AssetID: "a1", CustodianID: "u1"},
		AssetName:    "Zoom H6",
		AssetTag:     "AUD-002",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"id", "asset_id", "custodian_id", "asset_name", "asset_tag"} {
		if _, ok := got[k]; !ok {
			t.Errorf("active custody JSON is missing %q", k)
		}
	}
	if _, nested := got["CustodyEvent"]; nested {
		t.Error("embedded CustodyEvent was nested instead of flattened")
	}
}

// Enum drift guard: the Go constants and the Postgres enum labels have to stay
// in sync, otherwise a query writes a value the database rejects at runtime.
func TestEnumConstantsMatchDatabase(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()

	// Values that exist in Go ahead of a migration that adds them. Empty since
	// the v1 migration landed; add to it only while a new label is in flight.
	pendingInDB := map[string]map[string]bool{}

	cases := []struct {
		enum string
		goes []string
	}{
		{"user_role", []string{string(RoleOwner), string(RoleExecutiveProducer), string(RoleProducer), string(RoleMember)}},
		{"asset_status", []string{
			string(StatusAvailable), string(StatusCheckedOut), string(StatusUnavailable),
			string(StatusReserved), string(StatusMaintenance), string(StatusRetired), string(StatusLost),
		}},
		{"booking_status", []string{
			string(BookingReserved), string(BookingActive), string(BookingReturned),
			string(BookingOverdue), string(BookingCancelled),
		}},
		{"location_type", []string{
			string(LocationBuilding), string(LocationFloor), string(LocationRoom),
			string(LocationShelf), string(LocationOther),
		}},
	}

	for _, c := range cases {
		t.Run(c.enum, func(t *testing.T) {
			rows, err := db.Pool.Query(ctx,
				`select e.enumlabel from pg_enum e
				 join pg_type t on t.oid = e.enumtypid
				 where t.typname = $1 order by e.enumsortorder`, c.enum)
			if err != nil {
				t.Fatalf("query enum labels: %v", err)
			}
			defer rows.Close()

			inDB := map[string]bool{}
			for rows.Next() {
				var label string
				if err := rows.Scan(&label); err != nil {
					t.Fatal(err)
				}
				inDB[label] = true
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(inDB) == 0 {
				t.Fatalf("enum %q does not exist in the database", c.enum)
			}

			inGo := map[string]bool{}
			for _, v := range c.goes {
				inGo[v] = true
			}
			for label := range inDB {
				if !inGo[label] {
					t.Errorf("database enum %s has label %q with no Go constant", c.enum, label)
				}
			}
			for v := range inGo {
				if !inDB[v] && !pendingInDB[c.enum][v] {
					t.Errorf("Go constant %q is not a label of database enum %s", v, c.enum)
				}
			}
		})
	}
}
