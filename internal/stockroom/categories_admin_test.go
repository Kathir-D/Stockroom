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

func TestUpdateCategory(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	root := insertTestCategory(t, db, testCategoryName("Root"), nil)
	other := insertTestCategory(t, db, testCategoryName("Other root"), nil)
	child := insertTestCategory(t, db, testCategoryName("Child"), &root.ID)
	grandchild := insertTestCategory(t, db, testCategoryName("Grandchild"), &child.ID)

	t.Run("rename leaves the node where it is", func(t *testing.T) {
		renamed := testCategoryName("Renamed")
		got, err := db.UpdateCategory(ctx, admin, child.ID, CategoryInput{Name: renamed, ParentID: &root.ID})
		if err != nil {
			t.Fatalf("UpdateCategory: %v", err)
		}
		if got.Name != renamed || got.ParentID == nil || *got.ParentID != root.ID {
			t.Errorf("rename produced %+v", got)
		}
		if got.SortOrder != child.SortOrder {
			t.Errorf("sort_order = %d, want it untouched at %d", got.SortOrder, child.SortOrder)
		}
	})

	t.Run("an explicit sort_order is how a level gets reordered", func(t *testing.T) {
		got, err := db.UpdateCategory(ctx, admin, child.ID,
			CategoryInput{Name: child.Name + " ordered", ParentID: &root.ID, SortOrder: intptr(42)})
		if err != nil {
			t.Fatalf("UpdateCategory: %v", err)
		}
		if got.SortOrder != 42 {
			t.Errorf("sort_order = %d, want 42", got.SortOrder)
		}
	})

	t.Run("a move refuses to break the three-level tree", func(t *testing.T) {
		// child still carries grandchild, so hanging it off a depth-2 node
		// would put grandchild at depth 4.
		deep := insertTestCategory(t, db, testCategoryName("Depth two"), &other.ID)
		_, err := db.UpdateCategory(ctx, admin, child.ID, CategoryInput{Name: child.Name, ParentID: &deep.ID})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("move that would reach depth 4 = %v, want ErrInvalid", err)
		}
		// The leaf itself fits there, so the same move one level down is fine.
		if _, err := db.UpdateCategory(ctx, admin, grandchild.ID,
			CategoryInput{Name: grandchild.Name, ParentID: &deep.ID}); err != nil {
			t.Errorf("moving a leaf to depth 3: %v", err)
		}
	})

	t.Run("a node cannot be moved inside itself", func(t *testing.T) {
		_, err := db.UpdateCategory(ctx, admin, root.ID, CategoryInput{Name: root.Name, ParentID: &root.ID})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "own parent") {
			t.Errorf("self-parent = %v, want ErrInvalid", err)
		}
		_, err = db.UpdateCategory(ctx, admin, root.ID, CategoryInput{Name: root.Name, ParentID: &child.ID})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("move under a descendant = %v, want ErrInvalid", err)
		}
	})

	t.Run("a move to a fresh level puts the node last there", func(t *testing.T) {
		moved := insertTestCategory(t, db, testCategoryName("Moving"), &root.ID)
		sibling := insertTestCategory(t, db, testCategoryName("Sitting"), &other.ID)
		if _, err := db.Pool.Exec(ctx, `update categories set sort_order = 7 where id = $1`, sibling.ID); err != nil {
			t.Fatal(err)
		}
		got, err := db.UpdateCategory(ctx, admin, moved.ID, CategoryInput{Name: moved.Name, ParentID: &other.ID})
		if err != nil {
			t.Fatalf("UpdateCategory: %v", err)
		}
		if got.SortOrder != 8 {
			t.Errorf("sort_order = %d, want 8, one past the sibling already there", got.SortOrder)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := db.UpdateCategory(ctx, admin, "0f3d3a9e-0000-4000-8000-000000000000", CategoryInput{Name: "Nope"})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("UpdateCategory on an unknown id = %v, want ErrNotFound", err)
		}
	})
}

func TestDeleteCategory(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	adminP := insertTestProfile(t, db, true, "admin-pw")
	admin := actorFor(adminP)

	root := insertTestCategory(t, db, testCategoryName("Deletable root"), nil)
	child := insertTestCategory(t, db, testCategoryName("Deletable child"), &root.ID)

	err := db.DeleteCategory(ctx, admin, root.ID)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "subcategor") {
		t.Errorf("deleting a node with children = %v, want a conflict saying so", err)
	}

	// assets.category_id is "on delete set null", so nothing but this check
	// stops a delete from leaving a pile of uncategorised units behind.
	asset, err := db.CreateAsset(ctx, admin, AssetInput{
		AssetTag:   fmt.Sprintf("CAT-%09d", rand.IntN(1_000_000_000)),
		Name:       "Filed here",
		CategoryID: &child.ID,
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	err = db.DeleteCategory(ctx, admin, child.ID)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "1 asset") {
		t.Errorf("deleting a node with assets = %v, want a conflict counting them", err)
	}

	if err := db.DeleteAsset(ctx, admin, asset.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteCategory(ctx, admin, child.ID); err != nil {
		t.Errorf("deleting an empty node: %v", err)
	}
	if err := db.DeleteCategory(ctx, admin, child.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting it twice = %v, want ErrNotFound", err)
	}
}

// The tree the browse filter reads has to reflect an edit immediately, in the
// order the edit asked for: sort_order is the whole reason the column exists.
func TestCategoryEditsShowUpInTheTree(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))

	root := insertTestCategory(t, db, testCategoryName("Ordered root"), nil)
	alpha := createTestCategory(t, db, admin, CategoryInput{Name: testCategoryName("Alpha"), ParentID: &root.ID})
	beta := createTestCategory(t, db, admin, CategoryInput{Name: testCategoryName("Beta"), ParentID: &root.ID})
	// Put beta first by hand, which is the only way to reorder a level.
	if _, err := db.UpdateCategory(ctx, admin, beta.ID,
		CategoryInput{Name: beta.Name, ParentID: &root.ID, SortOrder: intptr(0)}); err != nil {
		t.Fatal(err)
	}

	tree, err := db.GetCategoryTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var children []CategoryNode
	for _, node := range tree {
		if node.ID == root.ID {
			children = node.Children
		}
	}
	if len(children) != 2 {
		t.Fatalf("tree has %d children under the test root, want 2", len(children))
	}
	if children[0].ID != beta.ID || children[1].ID != alpha.ID {
		t.Errorf("children are %q then %q, want the hand-set order", children[0].Name, children[1].Name)
	}
}

func intptr(i int) *int { return &i }
