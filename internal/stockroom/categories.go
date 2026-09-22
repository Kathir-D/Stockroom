package stockroom

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// The category tree is the browse screen's left-hand filter: Type ->
// Category -> Model, three levels deep, held in one table via parent_id
// (CLAUDE.md §6.2). It is a few dozen rows, so the whole table is read in one
// query and shaped in Go rather than with recursive SQL.
//
// categoryTree is the one shape that read produces. Every question the
// package asks about the tree goes through it: the nested filter tree, an
// asset's category path and sort key, which nodes sit under a filter, how
// deep a node is, what its next sibling position would be. The admin writes
// in categories_admin.go and the browse reads in assets.go share it, so they
// cannot disagree about what the tree looks like.

// CategoryNode is one node of the filter tree: a category row plus its
// children, nested to whatever depth the data has. Children is never null in
// JSON, so the frontend can recurse without a nil check.
type CategoryNode struct {
	Category
	Children []CategoryNode `json:"children"`
}

// CategoryRef is one step of a category path, trimmed to what a label needs.
type CategoryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GetCategoryTree returns the whole tree in one call, roots first, every
// level in its own sibling order (examples/categories.media-department.md's document order for the
// Types, so the filter reads Cameras/Bodies, Lenses, Lights, ... rather than
// alphabetically). Any full session may read it (CLAUDE.md §7).
func (db *DB) GetCategoryTree(ctx context.Context, actor Actor) ([]CategoryNode, error) {
	if err := RequireFullSession(actor); err != nil {
		return nil, err
	}
	tree, err := loadCategoryTree(ctx, db.Pool)
	if err != nil {
		return nil, err
	}
	return tree.nodes(), nil
}

// categoryColumns is the select list every category query uses, in the order
// scanCategory expects.
const categoryColumns = `id, name, parent_id, sort_order, created_at`

func scanCategory(row pgx.Row) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Name, &c.ParentID, &c.SortOrder, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("scan category: %w", err)
	}
	return c, nil
}

// loadCategoryTree reads every category row in display order (sort_order
// within a parent, name to break a tie) and shapes it. It takes a querier
// rather than the pool so the admin writes can measure the tree inside the
// transaction that is about to change it.
func loadCategoryTree(ctx context.Context, q querier) (categoryTree, error) {
	rows, err := q.Query(ctx,
		`select `+categoryColumns+` from categories order by sort_order, name`)
	if err != nil {
		return categoryTree{}, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	cats := []Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return categoryTree{}, err
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return categoryTree{}, fmt.Errorf("list categories: %w", err)
	}
	return newCategoryTree(cats), nil
}

// categoryTree is the category table after one walk of its parent links.
//
// A row whose parent is missing from the table, or which is its own parent,
// is treated as a root rather than dropped, so a filter tree is never silently
// short a branch and a malformed row is measured rather than skipped. Every
// walk guards against cycles, so a corrupt parent chain terminates.
type categoryTree struct {
	byID     map[string]Category
	children map[string][]string // parent id -> child ids in display order; "" holds the roots
	paths    map[string][]CategoryRef
	keys     map[string][]categorySortKey
	depth    map[string]int // 1 for a Type at the root
	height   map[string]int // 0 for a node with no children
}

// categorySortKey is one level of an asset's browse position: the node's
// sort_order among its siblings, then its name to break a tie. A whole path
// compares level by level from the root.
type categorySortKey struct {
	order int
	name  string
}

func newCategoryTree(cats []Category) categoryTree {
	t := categoryTree{
		byID:     make(map[string]Category, len(cats)),
		children: make(map[string][]string, len(cats)),
		paths:    make(map[string][]CategoryRef, len(cats)),
		keys:     make(map[string][]categorySortKey, len(cats)),
		depth:    make(map[string]int, len(cats)),
		height:   make(map[string]int, len(cats)),
	}
	for _, c := range cats {
		t.byID[c.ID] = c
	}
	for _, c := range cats {
		key := t.parentKey(c)
		t.children[key] = append(t.children[key], c.ID)
	}

	for _, c := range cats {
		path := []CategoryRef{}
		key := []categorySortKey{}
		seen := make(map[string]bool)
		for node := c; !seen[node.ID]; {
			seen[node.ID] = true
			path = append(path, CategoryRef{ID: node.ID, Name: node.Name})
			key = append(key, categorySortKey{order: node.SortOrder, name: node.Name})
			parent, ok := t.byID[t.parentKey(node)]
			if !ok {
				break
			}
			node = parent
		}
		slices.Reverse(path)
		slices.Reverse(key)
		t.paths[c.ID] = path
		t.keys[c.ID] = key
		t.depth[c.ID] = len(path)
	}

	var measure func(id string, seen map[string]bool) int
	measure = func(id string, seen map[string]bool) int {
		if h, done := t.height[id]; done {
			return h
		}
		if seen[id] {
			return 0
		}
		seen[id] = true
		h := 0
		for _, child := range t.children[id] {
			if ch := measure(child, seen) + 1; ch > h {
				h = ch
			}
		}
		t.height[id] = h
		return h
	}
	for _, c := range cats {
		measure(c.ID, map[string]bool{})
	}
	return t
}

// parentKey is the children-map key for c's parent: "" when c is a root by
// any of the three readings above.
func (t categoryTree) parentKey(c Category) string {
	if c.ParentID == nil || *c.ParentID == c.ID {
		return ""
	}
	if _, ok := t.byID[*c.ParentID]; !ok {
		return ""
	}
	return *c.ParentID
}

// nodes nests the tree for the filter, roots first.
func (t categoryTree) nodes() []CategoryNode {
	var nest func(id string) CategoryNode
	nest = func(id string) CategoryNode {
		node := CategoryNode{Category: t.byID[id], Children: []CategoryNode{}}
		for _, child := range t.children[id] {
			node.Children = append(node.Children, nest(child))
		}
		return node
	}
	roots := t.children[""]
	out := make([]CategoryNode, 0, len(roots))
	for _, id := range roots {
		out = append(out, nest(id))
	}
	return out
}

// require is the node with that id, or the not-found error every caller
// would otherwise word for itself. It is the only way code outside this
// file reads byID, so the maps stay private to the tree.
func (t categoryTree) require(id string) (Category, error) {
	c, ok := t.byID[id]
	if !ok {
		return Category{}, fmt.Errorf("%w: category %s", ErrNotFound, id)
	}
	return c, nil
}

// depthOf is how deep a node sits: 1 for a root, 0 for an unknown id.
func (t categoryTree) depthOf(id string) int {
	return t.depth[id]
}

// keyOf is the children-map key for an optional parent id: "" for the root.
func keyOf(parent *string) string {
	if parent == nil {
		return ""
	}
	return *parent
}

// pathOf is the root-to-node path for an asset's category_path. Unknown or
// nil ids give an empty, non-nil slice so the JSON is [] rather than null.
func (t categoryTree) pathOf(id *string) []CategoryRef {
	if id != nil {
		if p, ok := t.paths[*id]; ok {
			return p
		}
	}
	return []CategoryRef{}
}

// keyFor is the sort key of the category an asset is filed under. An asset
// with no category, or one naming a category that has since been deleted,
// gets the empty key, which sorts last.
func (t categoryTree) keyFor(id *string) []categorySortKey {
	if id == nil {
		return nil
	}
	return t.keys[*id]
}

// descendants is id and everything under it, which is what a category filter
// means: picking a Type shows every Model beneath it.
func (t categoryTree) descendants(id string) []string {
	out := []string{}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(cur string) {
		if seen[cur] {
			return
		}
		seen[cur] = true
		out = append(out, cur)
		for _, child := range t.children[cur] {
			walk(child)
		}
	}
	walk(id)
	return out
}

// isDescendant reports whether id sits anywhere under ancestor.
func (t categoryTree) isDescendant(id, ancestor string) bool {
	seen := map[string]bool{}
	for cur := id; cur != "" && !seen[cur]; {
		seen[cur] = true
		parent := t.parentKey(t.byID[cur])
		if parent == ancestor {
			return true
		}
		cur = parent
	}
	return false
}

// nextSortOrder is where a new node goes under parent (nil for the root): one
// past the largest sort_order already there, so it lands last. Gaps and
// duplicates in the column are fine; only the relative order matters.
func (t categoryTree) nextSortOrder(parent *string) int {
	next := 1
	for _, id := range t.children[keyOf(parent)] {
		if order := t.byID[id].SortOrder; order >= next {
			next = order + 1
		}
	}
	return next
}

// compareCategoryKeys orders two browse positions. A longer path never
// precedes its own prefix in practice (an asset is filed at one node), and an
// empty key (no category) sorts after every real one.
func compareCategoryKeys(a, b []categorySortKey) int {
	if len(a) == 0 || len(b) == 0 {
		return cmp.Compare(len(b), len(a))
	}
	return slices.CompareFunc(a, b, func(x, y categorySortKey) int {
		if c := cmp.Compare(x.order, y.order); c != 0 {
			return c
		}
		return cmp.Compare(x.name, y.name)
	})
}
