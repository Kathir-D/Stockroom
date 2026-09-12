package stockroom

import (
	"context"
	"fmt"
	"slices"
)

// The category tree is the browse screen's left-hand filter: Type ->
// Category -> Model, three levels deep, held in one table via parent_id
// (CLAUDE.md §6.2). It is small -- a few dozen rows -- so the whole thing is
// read in one query and shaped in Go rather than with recursive SQL. Both the
// tree and the per-asset category path come out of that one read.

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
// level sorted by name.
func (db *DB) GetCategoryTree(ctx context.Context) ([]CategoryNode, error) {
	cats, err := db.loadCategories(ctx)
	if err != nil {
		return nil, err
	}
	return buildCategoryTree(cats), nil
}

// loadCategories reads every category row, sorted by name so the tree and
// the paths built from it come out in a stable order.
func (db *DB) loadCategories(ctx context.Context) ([]Category, error) {
	rows, err := db.Pool.Query(ctx,
		`select id, name, parent_id, created_at from categories order by name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	cats := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.ParentID, &c.CreatedAt); err != nil {
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

// categoryPaths maps every category id to its path from the root down to
// itself, which is what an asset row shows as "Lenses / Zooms / Tamron
// 18-400mm". The walk upward stops on a repeat, so a malformed parent chain
// yields a short path instead of hanging.
func categoryPaths(cats []Category) map[string][]CategoryRef {
	byID := make(map[string]Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}

	paths := make(map[string][]CategoryRef, len(cats))
	for _, c := range cats {
		path := []CategoryRef{}
		seen := make(map[string]bool)
		for node := c; !seen[node.ID]; {
			seen[node.ID] = true
			path = append(path, CategoryRef{ID: node.ID, Name: node.Name})
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
		paths[c.ID] = path
	}
	return paths
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
