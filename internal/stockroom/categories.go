package stockroom

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// The category tree is the browse screen's left-hand filter: Type ->
// Category -> Model, three levels deep, held in one table via parent_id
// (CLAUDE.md §6.2). It is small -- a few dozen rows -- so the whole thing is
// read in one query and shaped in Go rather than with recursive SQL. The tree,
// the per-asset category path and the browse list's sort order all come out of
// that one read.

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
// level in its own sibling order (Catagories.md's document order for the
// Types, so the filter reads Cameras/Bodies, Lenses, Lights, ... rather than
// alphabetically).
func (db *DB) GetCategoryTree(ctx context.Context) ([]CategoryNode, error) {
	cats, err := db.loadCategories(ctx)
	if err != nil {
		return nil, err
	}
	return buildCategoryTree(cats), nil
}

// loadCategories reads every category row in display order: sort_order within
// a parent, name to break a tie. One read feeds the tree, the per-asset
// category path and the browse list's sort key, so those three can't disagree
// about what order the tree is in.
func (db *DB) loadCategories(ctx context.Context) ([]Category, error) {
	rows, err := db.Pool.Query(ctx,
		`select id, name, parent_id, sort_order, created_at from categories order by sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	cats := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.ParentID, &c.SortOrder, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	return cats, nil
}

// buildCategoryTree nests a flat list by parent_id. A row whose parent is
// missing from the list, or which is its own parent, is treated as a root
// rather than dropped, so a filter tree is never silently short a branch.
//
// Descending from the roots cannot loop: every member of a parent_id cycle
// has a parent inside that cycle, so no root reaches one. Rows in such a
// cycle are unreachable and stay out of the tree; nothing in the schema
// creates them.
func buildCategoryTree(cats []Category) []CategoryNode {
	present := make(map[string]bool, len(cats))
	for _, c := range cats {
		present[c.ID] = true
	}

	roots := []Category{}
	children := make(map[string][]Category)
	for _, c := range cats {
		if c.ParentID == nil || *c.ParentID == c.ID || !present[*c.ParentID] {
			roots = append(roots, c)
			continue
		}
		children[*c.ParentID] = append(children[*c.ParentID], c)
	}

	var nest func(c Category) CategoryNode
	nest = func(c Category) CategoryNode {
		node := CategoryNode{Category: c, Children: []CategoryNode{}}
		for _, child := range children[c.ID] {
			node.Children = append(node.Children, nest(child))
		}
		return node
	}

	tree := make([]CategoryNode, 0, len(roots))
	for _, r := range roots {
		tree = append(tree, nest(r))
	}
	return tree
}

// categoryIndex is the category table shaped for the browse screen. One walk
// upward from every node produces both things an asset row needs: the path it
// displays ("Lenses / Zooms / Tamron 18-400mm") and that same path as a sort
// key, so the list can be put in tree order in Go instead of with a recursive
// order-by.
type categoryIndex struct {
	paths map[string][]CategoryRef
	keys  map[string][]categorySortKey
}

// categorySortKey is one step of that sort key: where the node sits among its
// siblings, with its name to break a tie. The name matters because sort_order
// is unique by convention only and defaults to 0 for a level nobody has
// numbered, which then reads alphabetically rather than arbitrarily.
type categorySortKey struct {
	order int
	name  string
}

// indexCategories maps every category id to its path from the root down to
// itself. The walk upward stops on a repeat, so a malformed parent chain
// yields a short path instead of hanging.
func indexCategories(cats []Category) categoryIndex {
	byID := make(map[string]Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}

	index := categoryIndex{
		paths: make(map[string][]CategoryRef, len(cats)),
		keys:  make(map[string][]categorySortKey, len(cats)),
	}
	for _, c := range cats {
		path := []CategoryRef{}
		key := []categorySortKey{}
		seen := make(map[string]bool)
		for node := c; !seen[node.ID]; {
			seen[node.ID] = true
			path = append(path, CategoryRef{ID: node.ID, Name: node.Name})
			key = append(key, categorySortKey{order: node.SortOrder, name: node.Name})
			if node.ParentID == nil {
				break
			}
			parent, ok := byID[*node.ParentID]
			if !ok {
				break
			}
			node = parent
		}
		slices.Reverse(path)
		slices.Reverse(key)
		index.paths[c.ID] = path
		index.keys[c.ID] = key
	}
	return index
}

// compareCategoryKeys orders two category paths the way the filter tree reads
// top to bottom: by the first step at which they differ. A path that is a
// prefix of the other comes first, and an empty path -- an asset filed under
// no category at all -- comes last, because it belongs to no group a user can
// point at in the tree.
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

// categoryExists reports whether id names a category, so a filter on an id
// that was deleted (or never existed) can be an error rather than an empty
// list the user reads as "no equipment here".
func (db *DB) categoryExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`select exists (select 1 from categories where id = $1)`, id).Scan(&exists)
	if err != nil {
		return false, mapPgError("check category", err)
	}
	return exists, nil
}
