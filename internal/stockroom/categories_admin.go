package stockroom

import (
	"context"
	"fmt"
	"strings"
)

// Admin edits to the category tree (design doc §8.7): rename a node, add a
// child, move a branch, delete an empty one. Admin-only, gated here rather
// than in the router. The reads these writes feed live in categories.go.
//
// Two rules shape every write. The tree is exactly Type -> Category -> Model,
// so nothing may end up deeper than MaxCategoryDepth; and sort_order is a
// position among siblings, so a new node goes last and a level can be
// renumbered by hand.

// MaxCategoryDepth is how deep the tree may go: Type (1) -> Category (2) ->
// Model (3). The browse filter is built around those three levels
// (CLAUDE.md §6.2), so a fourth would have nowhere to render.
const MaxCategoryDepth = 3

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

	shape, err := db.categoryShape(ctx)
	if err != nil {
		return Category{}, err
	}
	if in.ParentID != nil {
		if _, ok := shape.byID[*in.ParentID]; !ok {
			return Category{}, fmt.Errorf("%w: category %s", ErrNotFound, *in.ParentID)
		}
		if shape.depth[*in.ParentID] >= MaxCategoryDepth {
			return Category{}, fmt.Errorf("%w: %q is already at the deepest level (%d), so it cannot have children",
				ErrInvalid, shape.byID[*in.ParentID].Name, MaxCategoryDepth)
		}
	}

	order := shape.nextSortOrder(in.ParentID)
	if in.SortOrder != nil {
		order = *in.SortOrder
	}

	c, err := scanCategory(db.Pool.QueryRow(ctx, `
		insert into categories (name, parent_id, sort_order)
		values ($1, $2, $3)
		returning `+categoryColumns, in.Name, in.ParentID, order))
	if err != nil {
		return Category{}, mapPgError("create category", err)
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

	shape, err := db.categoryShape(ctx)
	if err != nil {
		return Category{}, err
	}
	node, ok := shape.byID[id]
	if !ok {
		return Category{}, fmt.Errorf("%w: no category %s", ErrNotFound, id)
	}
	if err := shape.checkMove(node, in.ParentID); err != nil {
		return Category{}, err
	}

	order := node.SortOrder
	switch {
	case in.SortOrder != nil:
		order = *in.SortOrder
	case !sameParent(node.ParentID, in.ParentID):
		// A node arriving in a level it has never been in has no position
		// there, and its old number means nothing among its new siblings.
		order = shape.nextSortOrder(in.ParentID)
	}

	c, err := scanCategory(db.Pool.QueryRow(ctx, `
		update categories set name = $2, parent_id = $3, sort_order = $4
		where id = $1
		returning `+categoryColumns, id, in.Name, in.ParentID, order))
	if err != nil {
		return Category{}, mapPgError("update category", err)
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

	var children, assets int
	err := db.Pool.QueryRow(ctx, `
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

	tag, err := db.Pool.Exec(ctx, `delete from categories where id = $1`, id)
	if err != nil {
		return mapPgError("delete category", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: no category %s", ErrNotFound, id)
	}
	return nil
}

// categoryTreeShape is the tree measured, which is what the writes above need
// and the reads don't: how deep each node sits, how tall the branch under it
// is, and who its siblings are. One read of the whole table (a few dozen
// rows) answers all three.
type categoryTreeShape struct {
	byID     map[string]Category
	depth    map[string]int      // 1 for a Type at the root
	height   map[string]int      // 0 for a node with no children
	children map[string][]string // parent id -> child ids; "" holds the roots
}

func (db *DB) categoryShape(ctx context.Context) (categoryTreeShape, error) {
	cats, err := db.loadCategories(ctx)
	if err != nil {
		return categoryTreeShape{}, err
	}
	return measureCategories(cats), nil
}

// measureCategories fills in the depths and heights. A row whose parent is
// missing, or which is its own parent, counts as a root, the same reading
// buildCategoryTree gives it, so a malformed row is measured rather than
// skipped.
func measureCategories(cats []Category) categoryTreeShape {
	s := categoryTreeShape{
		byID:     make(map[string]Category, len(cats)),
		depth:    make(map[string]int, len(cats)),
		height:   make(map[string]int, len(cats)),
		children: make(map[string][]string, len(cats)),
	}
	for _, c := range cats {
		s.byID[c.ID] = c
	}
	for _, c := range cats {
		s.children[s.parentKey(c)] = append(s.children[s.parentKey(c)], c.ID)
	}

	// Depth walks up. The seen set stops a parent_id cycle, which nothing
	// writes but which would otherwise hang the loop.
	for _, c := range cats {
		depth, seen := 0, map[string]bool{}
		for node := c; !seen[node.ID]; {
			seen[node.ID] = true
			depth++
			parent, ok := s.byID[s.parentKey(node)]
			if !ok {
				break
			}
			node = parent
		}
		s.depth[c.ID] = depth
	}

	// Height walks down, memoised, with the same cycle guard.
	var measure func(id string, seen map[string]bool) int
	measure = func(id string, seen map[string]bool) int {
		if h, done := s.height[id]; done {
			return h
		}
		if seen[id] {
			return 0
		}
		seen[id] = true
		h := 0
		for _, child := range s.children[id] {
			if ch := measure(child, seen) + 1; ch > h {
				h = ch
			}
		}
		s.height[id] = h
		return h
	}
	for _, c := range cats {
		measure(c.ID, map[string]bool{})
	}
	return s
}

// parentKey is the children map's key for c: its parent id, or "" when it is
// a root by any of the three readings (no parent, a parent that isn't in the
// table, or itself).
func (s categoryTreeShape) parentKey(c Category) string {
	if c.ParentID == nil || *c.ParentID == c.ID {
		return ""
	}
	if _, ok := s.byID[*c.ParentID]; !ok {
		return ""
	}
	return *c.ParentID
}

// nextSortOrder is one past the highest position among the children of
// parent, so a new node lands after its siblings. Gaps and duplicates are
// harmless (the name breaks a tie), so nothing renumbers the level.
func (s categoryTreeShape) nextSortOrder(parent *string) int {
	key := ""
	if parent != nil {
		key = *parent
	}
	next := 1
	for _, id := range s.children[key] {
		if order := s.byID[id].SortOrder; order >= next {
			next = order + 1
		}
	}
	return next
}

// checkMove decides whether node may hang off parent: not itself, not one of
// its own descendants, and not so deep that the branch under it runs past
// MaxCategoryDepth.
func (s categoryTreeShape) checkMove(node Category, parent *string) error {
	if parent == nil {
		if 1+s.height[node.ID] > MaxCategoryDepth {
			return s.tooDeep(node, 1)
		}
		return nil
	}
	if *parent == node.ID {
		return fmt.Errorf("%w: a category cannot be its own parent", ErrInvalid)
	}
	target, ok := s.byID[*parent]
	if !ok {
		return fmt.Errorf("%w: category %s", ErrNotFound, *parent)
	}
	if s.isDescendant(*parent, node.ID) {
		return fmt.Errorf("%w: cannot move %q under %q, which is inside it", ErrInvalid, node.Name, target.Name)
	}
	if depth := s.depth[*parent] + 1; depth+s.height[node.ID] > MaxCategoryDepth {
		return s.tooDeep(node, depth)
	}
	return nil
}

// isDescendant reports whether id sits somewhere under ancestor.
func (s categoryTreeShape) isDescendant(id, ancestor string) bool {
	seen := map[string]bool{}
	for cur := id; cur != "" && !seen[cur]; {
		seen[cur] = true
		parent := s.parentKey(s.byID[cur])
		if parent == ancestor {
			return true
		}
		cur = parent
	}
	return false
}

func (s categoryTreeShape) tooDeep(node Category, depth int) error {
	return fmt.Errorf("%w: the tree is %d levels deep, and that move would put %q at level %d with %d more below it",
		ErrInvalid, MaxCategoryDepth, node.Name, depth, s.height[node.ID])
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
