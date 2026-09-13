package stockroom

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Admin edits to the category tree (design doc §8.7): rename a node, add a
// child, move a branch, delete an empty one. Admin-only, gated here rather
// than in the router. The reads these writes feed live in categories.go.
//
// Two rules shape every write. The tree is exactly Type -> Category -> Model,
// so nothing may end up deeper than MaxCategoryDepth; and sort_order is a
// position among siblings, so a new node goes last and a level can be
// renumbered by hand.
//
// Both rules are decided in Go against a snapshot of the whole table, which
// only holds if one write happens at a time, so all three take
// categoryWriteLock and keep it to the commit.

// MaxCategoryDepth is how deep the tree may go: Type (1) -> Category (2) ->
// Model (3). The browse filter is built around those three levels
// (CLAUDE.md §6.2), so a fourth would have nowhere to render.
const MaxCategoryDepth = 3

// categoryWriteLock is the advisory lock every category write takes before it
// measures the tree. The depth and cycle checks below run in Go over a
// snapshot of the table, so two writes that each read a legal tree can commit
// an illegal one between them: two moves that make each other's node their
// parent both pass, and together they leave a cycle no read can draw. The
// tree is a few dozen rows edited by one admin at a time, so serialising the
// writes costs nothing and removes the whole class.
//
// The number is arbitrary. It only has to be one nothing else in this
// database uses, and this package takes no other advisory lock.
const categoryWriteLock = 20260913

// beginCategoryWrite opens a transaction already holding categoryWriteLock.
// The lock is transaction-scoped, so committing or rolling back always
// releases it; nothing has to remember to.
func (db *DB) beginCategoryWrite(ctx context.Context, what string) (pgx.Tx, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, categoryWriteLock); err != nil {
		_ = tx.Rollback(ctx)
		return nil, mapPgError(what, err)
	}
	return tx, nil
}

// CategoryInput is the create/edit payload for one node.
type CategoryInput struct {
	Name string `json:"name"`
	// ParentID is where the node sits. Null means a Type, at the root of the
	// tree. On an update it is a move: the node and everything under it
	// change parents, which is why the depth check below measures the whole
	// subtree rather than just the node.
	ParentID *string `json:"parent_id"`
	// SortOrder is the node's position among its siblings. Null means "put it
	// last": a new node lands after its siblings, and an edit that doesn't
	// mention the order leaves it where it was. Sending a number is how a
	// level gets renumbered by hand, which is the only way to reorder one.
	SortOrder *int `json:"sort_order"`
}

func (in *CategoryInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("%w: a category name is required", ErrInvalid)
	}
	in.ParentID = trimOptional(in.ParentID)
	return nil
}

// CreateCategory adds a node under parent, or a new Type when parent is null.
// The name is unique across the whole table, not just among siblings
// (CLAUDE.md §6.2), so a second "Other" is ErrConflict wherever it sits.
func (db *DB) CreateCategory(ctx context.Context, actor Actor, in CategoryInput) (Category, error) {
	if err := RequireAdmin(actor); err != nil {
		return Category{}, err
	}
	if err := in.normalize(); err != nil {
		return Category{}, err
	}

	tx, err := db.beginCategoryWrite(ctx, "create category")
	if err != nil {
		return Category{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tree, err := loadCategoryTree(ctx, tx)
	if err != nil {
		return Category{}, err
	}
	if in.ParentID != nil {
		parent, err := tree.require(*in.ParentID)
		if err != nil {
			return Category{}, err
		}
		if tree.depthOf(parent.ID) >= MaxCategoryDepth {
			return Category{}, fmt.Errorf("%w: %q is already at the deepest level (%d), so it cannot have children",
				ErrInvalid, parent.Name, MaxCategoryDepth)
		}
	}

	order := tree.nextSortOrder(in.ParentID)
	if in.SortOrder != nil {
		order = *in.SortOrder
	}

	c, err := scanCategory(tx.QueryRow(ctx, `
		insert into categories (name, parent_id, sort_order)
		values ($1, $2, $3)
		returning `+categoryColumns, in.Name, in.ParentID, order))
	if err != nil {
		return Category{}, mapPgError("create category", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Category{}, fmt.Errorf("create category: %w", err)
	}
	return c, nil
}

// UpdateCategory renames a node, moves it, or renumbers it. A move is refused
// when it would put the branch deeper than MaxCategoryDepth or under one of
// its own descendants; both would leave a tree the browse screen cannot draw.
func (db *DB) UpdateCategory(ctx context.Context, actor Actor, id string, in CategoryInput) (Category, error) {
	if err := RequireAdmin(actor); err != nil {
		return Category{}, err
	}
	if err := in.normalize(); err != nil {
		return Category{}, err
	}

	tx, err := db.beginCategoryWrite(ctx, "update category")
	if err != nil {
		return Category{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tree, err := loadCategoryTree(ctx, tx)
	if err != nil {
		return Category{}, err
	}
	node, err := tree.require(id)
	if err != nil {
		return Category{}, err
	}
	if err := tree.checkMove(node, in.ParentID); err != nil {
		return Category{}, err
	}

	order := node.SortOrder
	switch {
	case in.SortOrder != nil:
		order = *in.SortOrder
	case !sameParent(node.ParentID, in.ParentID):
		// A node arriving in a level it has never been in has no position
		// there, and its old number means nothing among its new siblings.
		order = tree.nextSortOrder(in.ParentID)
	}

	c, err := scanCategory(tx.QueryRow(ctx, `
		update categories set name = $2, parent_id = $3, sort_order = $4
		where id = $1
		returning `+categoryColumns, id, in.Name, in.ParentID, order))
	if err != nil {
		return Category{}, mapPgError("update category", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Category{}, fmt.Errorf("update category: %w", err)
	}
	return c, nil
}

// DeleteCategory removes an empty node. A node with children or with assets
// filed under it is refused, and the message says which of the two it is,
// because the fix differs: move the children out, or re-file the units.
//
// Refusing on assets is this function's job, not the schema's:
// assets.category_id is "on delete set null", so the database would happily
// leave a pile of uncategorised units behind.
func (db *DB) DeleteCategory(ctx context.Context, actor Actor, id string) error {
	if err := RequireAdmin(actor); err != nil {
		return err
	}

	tx, err := db.beginCategoryWrite(ctx, "delete category")
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the row before counting what hangs off it. The lock above already
	// holds off another category write, but CreateAsset takes no part in it,
	// and its foreign-key check would otherwise be free to file a unit under
	// a node between the count and the delete -- leaving the uncategorised
	// pile this refusal exists to prevent.
	var locked string
	err = tx.QueryRow(ctx, `select id from categories where id = $1 for update`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no category %s", ErrNotFound, id)
	}
	if err != nil {
		return mapPgError("delete category", err)
	}

	var children, assets int
	err = tx.QueryRow(ctx, `
		select (select count(*) from categories where parent_id = $1),
		       (select count(*) from assets where category_id = $1)`, id).Scan(&children, &assets)
	if err != nil {
		return mapPgError("delete category", err)
	}
	if children > 0 {
		return fmt.Errorf("%w: category has %s under it", ErrConflict, plural(children, "subcategory", "subcategories"))
	}
	if assets > 0 {
		return fmt.Errorf("%w: category has %s filed under it", ErrConflict, plural(assets, "asset", "assets"))
	}

	if _, err := tx.Exec(ctx, `delete from categories where id = $1`, id); err != nil {
		return mapPgError("delete category", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	return nil
}

// checkMove decides whether node may hang off parent: not itself, not one of
// its own descendants, and not so deep that the branch under it runs past
// MaxCategoryDepth.
func (t categoryTree) checkMove(node Category, parent *string) error {
	if parent == nil {
		if 1+t.height[node.ID] > MaxCategoryDepth {
			return t.tooDeep(node, 1)
		}
		return nil
	}
	if *parent == node.ID {
		return fmt.Errorf("%w: a category cannot be its own parent", ErrInvalid)
	}
	target, err := t.require(*parent)
	if err != nil {
		return err
	}
	if t.isDescendant(*parent, node.ID) {
		return fmt.Errorf("%w: cannot move %q under %q, which is inside it", ErrInvalid, node.Name, target.Name)
	}
	if depth := t.depthOf(target.ID) + 1; depth+t.height[node.ID] > MaxCategoryDepth {
		return t.tooDeep(node, depth)
	}
	return nil
}

// tooDeep words the depth refusal so an admin sees why the move failed.
func (t categoryTree) tooDeep(node Category, depth int) error {
	return fmt.Errorf("%w: the tree is %d levels deep, and that move would put %q at level %d with %d more below it",
		ErrInvalid, MaxCategoryDepth, node.Name, depth, t.height[node.ID])
}

// sameParent compares two optional parent ids, treating null as the root.
func sameParent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// plural picks the singular or plural form and prefixes the count, so a
// message reads "1 asset" rather than "1 assets".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
