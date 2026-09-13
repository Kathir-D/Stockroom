package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// Phase 5's category writes. The seeded tree is shared by every test in the
// package, so each case here builds its own branch under a uniquely named
// root and deletes it again; category names are unique across the whole
// table, not per parent, so the names have to be unique too.

// insertTestCategory adds a node directly, for cases that need a tree to act
// on rather than a tree built by the function under test.
func insertTestCategory(t *testing.T, db *DB, name string, parent *string) Category {
	t.Helper()
	c, err := scanCategory(db.Pool.QueryRow(context.Background(), `
		insert into categories (name, parent_id) values ($1, $2)
		returning `+categoryColumns, name, parent))
	if err != nil {
		t.Fatalf("insert test category: %v", err)
	}
	dropCategoryAfter(t, db, c.ID)
	return c
}

// createTestCategory goes through CreateCategory itself and cleans up after
// it, so a test can build a branch with the function under test without
// leaving nodes in the tree every other test reads.
func createTestCategory(t *testing.T, db *DB, admin Actor, in CategoryInput) Category {
	t.Helper()
	c, err := db.CreateCategory(context.Background(), admin, in)
	if err != nil {
		t.Fatalf("CreateCategory(%q): %v", in.Name, err)
	}
	dropCategoryAfter(t, db, c.ID)
	return c
}

// dropCategoryAfter removes a node at the end of the test. Cleanups run last
// in first out, so a child registered after its parent goes first; the
// children sweep covers a node moved under this one mid-test, which would
// otherwise be left as a stray root in every later test's tree
// (parent_id is "on delete set null").
func dropCategoryAfter(t *testing.T, db *DB, id string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = db.Pool.Exec(ctx, `delete from categories where parent_id = $1`, id)
		_, _ = db.Pool.Exec(ctx, `delete from categories where id = $1`, id)
	})
}

// testCategoryName is a name no other row is using, since the column is
// unique table-wide rather than per parent.
func testCategoryName(label string) string {
	return fmt.Sprintf("ZZ Test %s %09d", label, rand.IntN(1_000_000_000))
}

func TestCategoryWritesAreAdminOnly(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	student := actorFor(insertTestProfile(t, db, false, "student-pw"))
	existing := insertTestCategory(t, db, testCategoryName("Guarded"), nil)

	_, err := db.CreateCategory(ctx, student, CategoryInput{Name: "Nope"})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("CreateCategory as a student = %v, want ErrForbidden", err)
	}
	_, err = db.UpdateCategory(ctx, student, existing.ID, CategoryInput{Name: "Nope"})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("UpdateCategory as a student = %v, want ErrForbidden", err)
	}
	if err := db.DeleteCategory(ctx, student, existing.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("DeleteCategory as a student = %v, want ErrForbidden", err)
	}
}

func TestCreateCategory(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	typeName := testCategoryName("Type")
	root := createTestCategory(t, db, admin, CategoryInput{Name: "  " + typeName + "  "})
	if root.Name != typeName {
		t.Errorf("name = %q, want it trimmed", root.Name)
	}
	if root.ParentID != nil {
		t.Errorf("parent_id = %v, want a root", *root.ParentID)
	}

	// A new node lands after its siblings: sort_order is a position, and the
	// browse filter reads the level in that order.
	first := createTestCategory(t, db, admin, CategoryInput{Name: testCategoryName("Cat A"), ParentID: &root.ID})
	second := createTestCategory(t, db, admin, CategoryInput{Name: testCategoryName("Cat B"), ParentID: &root.ID})
	if first.SortOrder != 1 || second.SortOrder != first.SortOrder+1 {
		t.Errorf("sort_order = %d then %d, want consecutive positions", first.SortOrder, second.SortOrder)
	}

	// Depth 3 is the Model level, and that is as deep as the tree goes.
	model := createTestCategory(t, db, admin, CategoryInput{Name: testCategoryName("Model"), ParentID: &first.ID})
	_, err := db.CreateCategory(ctx, admin, CategoryInput{Name: testCategoryName("Too deep"), ParentID: &model.ID})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "deepest level") {
		t.Errorf("CreateCategory at depth 4 = %v, want ErrInvalid naming the limit", err)
	}

	t.Run("rejects", func(t *testing.T) {
		unknown := "0f3d3a9e-0000-4000-8000-000000000000"
		if _, err := db.CreateCategory(ctx, admin, CategoryInput{Name: "   "}); !errors.Is(err, ErrInvalid) {
			t.Errorf("blank name = %v, want ErrInvalid", err)
		}
		if _, err := db.CreateCategory(ctx, admin, CategoryInput{Name: typeName}); !errors.Is(err, ErrConflict) {
			t.Errorf("duplicate name = %v, want ErrConflict", err)
		}
		_, err := db.CreateCategory(ctx, admin, CategoryInput{Name: testCategoryName("Orphan"), ParentID: &unknown})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown parent = %v, want ErrNotFound", err)
		}
	})
}
