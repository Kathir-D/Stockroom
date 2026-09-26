package stockroom

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Importing a category tree in one go (CLAUDE.md §13, Phase B).
//
// This exists because building an eight-Type tree through the *New category*
// dialog is roughly sixty dialogs, it is the **first** thing a new school
// does, and it is therefore exactly where they stop. Nothing else in Phase B
// matters if the catalogue is empty.
//
// Two input shapes, sniffed rather than declared, because a department has one
// or the other already and neither is worth retyping:
//
//   - **Indented text**, the shape `examples/categories.media-department.md` and the files in examples/
//     use: Markdown headings and list items, or plain indentation. This keeps
//     the example files readable as documents rather than as data.
//   - **CSV** with a `type,category,model` header, which is what a department
//     more often already has, out of a spreadsheet.
//
// It is **idempotent**: re-running adds what is missing and changes nothing
// else. That is not a nicety -- the first attempt will have a typo in it, and
// the fix has to be "correct the file and import again" rather than "delete
// sixty rows by hand first".

// CategoryImportResult is what the screen reports back.
type CategoryImportResult struct {
	Created int `json:"created"`
	// Existing is how many nodes in the file were already there. It is
	// reported rather than folded into Created because on a *second* run the
	// interesting number is that nothing changed, and "0 created" alone reads
	// like the import failed.
	Existing int                  `json:"existing"`
	Rows     []CategoryImportRow  `json:"rows"`
	Failed   int                  `json:"failed"`
	Format   CategoryImportFormat `json:"format"`
}

// CategoryImportRow is one node the file asked for.
type CategoryImportRow struct {
	Path    []string `json:"path"`
	Created bool     `json:"created"`
	Error   string   `json:"error,omitempty"`
}

type CategoryImportFormat string

const (
	CategoryImportText CategoryImportFormat = "text"
	CategoryImportCSV  CategoryImportFormat = "csv"
)

// ImportCategories creates every node named by the file that does not already
// exist, in document order.
//
// Document order is the point of the `sort_order` column (CLAUDE.md §13,
// 2026-09-13): a browse sidebar sorted alphabetically puts Audio above Cameras
// and reads as wrong to everybody who knows the cupboard. New siblings are
// appended in the order the file lists them.
//
// The whole import is one transaction. Unlike the roster, where a bad row is
// skipped and the rest land, a half-built tree is worse than none: a Model
// whose parent failed leaves assets with nowhere sensible to file, and the
// admin cannot tell by looking which half arrived.
func (db *DB) ImportCategories(ctx context.Context, actor Actor, r io.Reader) (CategoryImportResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return CategoryImportResult{}, err
	}

	body, err := readCapped(r, maxCategoryImportBytes)
	if err != nil {
		return CategoryImportResult{}, err
	}

	format, paths, err := parseCategoryFile(string(body))
	if err != nil {
		return CategoryImportResult{}, err
	}
	if len(paths) == 0 {
		return CategoryImportResult{}, fmt.Errorf("%w: that file has no categories in it", ErrInvalid)
	}

	res := CategoryImportResult{Format: format}

	tx, err := db.beginCategoryWrite(ctx, "import categories")
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tree, err := loadCategoryTree(ctx, tx)
	if err != nil {
		return res, err
	}

	// created maps a full path to the id of the node at it, so a Type named
	// once at the top of the file is reused by every Model under it without a
	// re-read.
	created := map[string]string{}
	for id := range tree.byID {
		key := make([]string, 0, MaxCategoryDepth)
		for _, ref := range tree.pathOf(&id) {
			key = append(key, strings.ToLower(ref.Name))
		}
		created[strings.Join(key, "\x00")] = id
	}

	for _, path := range paths {
		row := CategoryImportRow{Path: path}
		id, madeAny, err := ensureCategoryPath(ctx, tx, created, path)
		switch {
		case err != nil:
			row.Error = err.Error()
			res.Failed++
		case madeAny:
			row.Created = true
			res.Created++
		default:
			res.Existing++
		}
		_ = id
		res.Rows = append(res.Rows, row)
	}

	if res.Failed > 0 {
		// Named, not counted. The overwhelmingly likely cause is the
		// whole-table uniqueness of categories.name (CLAUDE.md §6.2) -- two
		// departments both wanting an "Accessories" node -- and a message that
		// does not say which line is a message nobody can act on.
		return res, fmt.Errorf("%w: %d of %d categories could not be created, so nothing was changed. First problem: %s",
			ErrInvalid, res.Failed, len(paths), firstRowError(res.Rows))
	}
	if err := tx.Commit(ctx); err != nil {
		return res, mapPgError("import categories", err)
	}
	db.logBestEffort(ctx, LogEntry{Category: LogAdmin, Action: "categories_imported", ActorID: actorLogID(actor),
		Summary: fmt.Sprintf("Imported categories: %d added, %d already there", res.Created, res.Existing),
		Details: map[string]any{"created": res.Created, "existing": res.Existing}})
	return res, nil
}

// maxCategoryImportBytes bounds the upload. A category tree is a few
// kilobytes of text; a megabyte is four hundred times the largest plausible
// one and still small enough to hold in memory without thinking about it.
const maxCategoryImportBytes = 1 << 20

func firstRowError(rows []CategoryImportRow) string {
	for _, r := range rows {
		if r.Error != "" {
			return strings.Join(r.Path, " > ") + " — " + r.Error
		}
	}
	return "unknown"
}

// ensureCategoryPath creates whatever part of path does not exist yet, and
// returns the id of the leaf plus whether anything was actually made.
//
// Matching is **case-insensitive on the full path**, so re-importing a file
// whose "Lenses" was typed "lenses" the second time finds the first one rather
// than failing on the unique index with a message about a constraint.
func ensureCategoryPath(ctx context.Context, tx pgx.Tx, created map[string]string, path []string) (string, bool, error) {
	var parentID *string
	madeAny := false

	for depth, name := range path {
		key := strings.ToLower(strings.Join(path[:depth+1], "\x00"))
		if id, ok := created[key]; ok {
			idCopy := id
			parentID = &idCopy
			continue
		}

		if depth >= MaxCategoryDepth {
			return "", madeAny, fmt.Errorf("the tree is only %d levels deep, and this needs %d",
				MaxCategoryDepth, depth+1)
		}

		// A savepoint per insert. Without one, the first unique violation
		// aborts the whole transaction and every later row fails with
		// "current transaction is aborted" -- so the report counted rows that
		// had nothing wrong with them as failures, and named the real problem
		// only by luck of it being first. The import still commits nothing
		// when anything failed; this only keeps the report honest.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return "", madeAny, mapPgError("create category", err)
		}
		var id string
		err = sp.QueryRow(ctx, `
			insert into categories (name, parent_id, sort_order)
			values ($1, $2, coalesce(
				(select max(sort_order) + 1 from categories
				  where parent_id is not distinct from $2), 0))
			returning id`, name, parentID).Scan(&id)
		if err == nil {
			err = sp.Commit(ctx)
		} else {
			_ = sp.Rollback(ctx)
		}
		if err != nil {
			mapped := mapPgError("create category", err)
			if errors.Is(mapped, ErrConflict) {
				// categories.name is unique across the whole table, not per
				// parent, and that is the one constraint this import trips
				// over in practice. Saying so is the difference between an
				// admin editing one line and an admin giving up.
				return "", madeAny, fmt.Errorf(
					"a category called %q already exists somewhere else in the tree — category names have to be unique across the whole tree, so try a more specific name", name)
			}
			return "", madeAny, mapped
		}

		created[key] = id
		idCopy := id
		parentID = &idCopy
		madeAny = true
	}

	if parentID == nil {
		return "", madeAny, fmt.Errorf("empty category path")
	}
	return *parentID, madeAny, nil
}

/* ----------------------------------------------------------- the parsers ---- */

// parseCategoryFile sniffs the shape and returns one path per leaf.
//
// Sniffing rather than a format parameter: the admin has a file, and being
// asked which of two formats it is before they can upload it is a question
// they should not have to answer about their own document.
func parseCategoryFile(body string) (CategoryImportFormat, [][]string, error) {
	// Excel puts a BOM in front of every UTF-8 CSV it saves. Left in, the
	// header's first cell is "\ufefftype", the sniff below says "not a CSV",
	// and the whole file becomes one category named after its header line
	// (the asset import strips it for the same reason).
	body = strings.TrimPrefix(body, "\ufeff")
	if looksLikeCategoryCSV(body) {
		paths, err := parseCategoryCSV(body)
		return CategoryImportCSV, paths, err
	}
	paths, err := parseCategoryText(body)
	return CategoryImportText, paths, err
}

// looksLikeCategoryCSV is true when the first non-blank line is a header
// naming the columns.
//
// Deliberately strict: it requires the word "type" in a comma-separated first
// line, rather than guessing from commas alone. A Markdown list item like
// "- Canon 24-70mm, 24-105mm" has a comma in it and is not a CSV, and guessing
// wrong turns a category tree into one category with a very long name.
func looksLikeCategoryCSV(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, ",") {
			return false
		}
		first := strings.ToLower(strings.TrimSpace(strings.SplitN(line, ",", 2)[0]))
		first = strings.Trim(first, "\"")
		return first == "type"
	}
	return false
}

func parseCategoryCSV(body string) ([][]string, error) {
	rd := csv.NewReader(strings.NewReader(body))
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true

	records, err := rd.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: that CSV could not be read: %v", ErrInvalid, err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("%w: that CSV has a header but no rows", ErrInvalid)
	}

	var paths [][]string
	for _, rec := range records[1:] {
		var path []string
		for _, field := range rec {
			field = strings.TrimSpace(field)
			if field == "" {
				// A blank cell ends the path rather than being skipped: a row
				// of `Lenses,,Canon 50mm` means a Model with no Category,
				// which the tree has no way to express, and silently filing it
				// under Lenses would put it somewhere the admin did not ask
				// for.
				break
			}
			path = append(path, field)
		}
		if len(path) > 0 {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// parseCategoryText reads the indented shape.
//
// It accepts both Markdown (`##` / `###` headings and `-` list items) and
// plain indentation, because the example files are Markdown and a person
// typing a tree by hand writes tabs. Depth comes from the marker when there is
// one and from the leading whitespace when there is not.
func parseCategoryText(body string) ([][]string, error) {
	var paths [][]string
	// stack[d] is the name at depth d of the branch being read.
	var stack []string

	// In a Markdown file only headings and list items are categories. Its
	// prose -- an intro paragraph, a note under a heading -- would otherwise
	// parse as indentation depth 0 and become a top-level Type, so an example
	// file with one sentence of explanation would import a Type called that
	// sentence.
	markdown := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			markdown = true
			break
		}
	}

	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), maxCategoryImportBytes)

	for sc.Scan() {
		raw := strings.TrimRight(sc.Text(), " \t\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}

		depth, name, ok := categoryLineDepth(raw)
		if !ok {
			continue
		}
		if markdown && !hasMarkdownMarker(raw) {
			continue
		}
		if name == "" {
			continue
		}
		if depth > len(stack) {
			// A line indented further than one level past its parent. Clamping
			// rather than failing: an extra space in a hand-written file is
			// not worth refusing a whole import over, and the result is the
			// node the person plainly meant.
			depth = len(stack)
		}
		stack = append(stack[:depth], name)

		// Every node becomes a path, not only leaves: a Type with no Models
		// under it yet is still a Type the admin wants, and `Primes` is
		// seeded exactly that way (CLAUDE.md §6.2).
		path := make([]string, len(stack))
		copy(path, stack)
		paths = append(paths, path)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: could not read that file: %v", ErrInvalid, err)
	}
	return paths, nil
}

// categoryLineDepth works out what level a line sits at.
//
// The `# Categories` title of a Markdown file is skipped rather than becoming
// a root node that everything else hangs under -- it is the document's title,
// not a category, and `examples/categories.media-department.md` opens with one.
func categoryLineDepth(raw string) (depth int, name string, ok bool) {
	trimmed := strings.TrimLeft(raw, " \t")
	indent := len(raw) - len(trimmed)

	switch {
	case strings.HasPrefix(trimmed, "#"):
		hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		name = strings.TrimSpace(trimmed[hashes:])
		if hashes <= 1 {
			// The document title.
			return 0, "", false
		}
		return hashes - 2, name, true

	case strings.HasPrefix(trimmed, "-"), strings.HasPrefix(trimmed, "*"), strings.HasPrefix(trimmed, "+"):
		name = strings.TrimSpace(trimmed[1:])
		// A list item under `###` is depth 2. Nested list items add a level
		// per two spaces of indent, which is what every Markdown editor emits.
		return 2 + indent/2, name, true

	default:
		// Plain indentation: a tab or two spaces per level.
		return indentDepth(raw), strings.TrimSpace(trimmed), true
	}
}

func indentDepth(raw string) int {
	depth := 0
	for _, r := range raw {
		switch r {
		case '\t':
			// Two, so a tab is one level after the halving below. Counted as
			// one it was half a level, and a single-tab child landed at the
			// top of the tree beside its own parent.
			depth += 2
		case ' ':
			depth++
		default:
			return depth / 2
		}
	}
	return 0
}

// hasMarkdownMarker is whether a line is a heading or a list item.
func hasMarkdownMarker(raw string) bool {
	t := strings.TrimLeft(raw, " \t")
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") ||
		strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ")
}
