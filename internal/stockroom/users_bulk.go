package stockroom

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Adding users from a pasted list of names, for a class, a club or a summer
// camp with no CSV to hand and often no ID numbers either.
//
// Each line is one person. A line may carry its own student number; one that
// doesn't gets the next free number from a range the admin starts, such as
// 900001 or G0001. Like "Add several" for assets, it previews first and
// writes nothing until the admin has seen every name and number.
//
// It only adds. A number that already belongs to somebody is an error on
// that line, not an update: renaming people is the roster import's job, and
// a pasted list that silently renamed an existing account would be a quiet
// way to hand one person's loans to another.

// AddNamesInput is the pasted list and where numbering starts.
type AddNamesInput struct {
	// Names is the pasted text, one person per line (parseNameLine).
	Names string `json:"names"`
	// FirstNumber is the first number to hand out to lines without one,
	// such as "900001" or "G0001". Its trailing digits count up and keep
	// their width. Blank means every line must carry its own number.
	FirstNumber string `json:"first_number"`
}

// AddNamesRow is one line of the list, as it will be or was added.
type AddNamesRow struct {
	Line          int    `json:"line"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	StudentNumber string `json:"student_number"`
	// Assigned is true when the number came from the range.
	Assigned bool   `json:"assigned"`
	Error    string `json:"error,omitempty"`
	// ID is set once the account exists.
	ID string `json:"id,omitempty"`
}

// AddNamesResult is the preview, or what was added.
type AddNamesResult struct {
	Rows   []AddNamesRow `json:"rows"`
	Failed int           `json:"failed"`
}

// maxAddNames bounds one paste. A whole year group is a few hundred.
const maxAddNames = 500

// PreviewAddNames works out every name and number without writing anything.
func (db *DB) PreviewAddNames(ctx context.Context, actor Actor, in AddNamesInput) (AddNamesResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return AddNamesResult{}, err
	}
	return db.planAddNames(ctx, db.Pool, in)
}

// AddNames creates the accounts, in one transaction, and refuses the whole
// list if any line has an error: the preview showed them, and half a class
// with numbers is harder to finish by hand than none.
func (db *DB) AddNames(ctx context.Context, actor Actor, in AddNamesInput) (AddNamesResult, error) {
	if err := RequireAdmin(actor); err != nil {
		return AddNamesResult{}, err
	}
	var res AddNamesResult
	err := db.withLoggedTx(ctx, actorLogID(actor), "add users", func(tx pgx.Tx) error {
		// Serialise with any other add, so two admins pressing Add at once
		// can't be handed the same range.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('stockroom.add_names'))`); err != nil {
			return mapPgError("add users", err)
		}
		var err error
		res, err = db.planAddNames(ctx, tx, in)
		if err != nil {
			return err
		}
		if res.Failed > 0 {
			return fmt.Errorf("%w: %d line(s) have a problem; fix them and preview again", ErrInvalid, res.Failed)
		}
		for i := range res.Rows {
			row := &res.Rows[i]
			p, err := scanProfile(tx.QueryRow(ctx, `
				insert into profiles (student_number, first_name, last_name, full_name)
				values ($1, $2, $3, $4)
				returning `+profileColumns,
				row.StudentNumber, row.FirstName, row.LastName, fullName(row.FirstName, row.LastName)))
			if err != nil {
				return fmt.Errorf("line %d: %w", row.Line, mapPgError("add user", err))
			}
			row.ID = p.ID
			if err := writeLog(ctx, tx, userLogEntry(actor, "user_created", "Created account for", p)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AddNamesResult{}, err
	}
	return res, nil
}

// planAddNames parses the list and gives every line its number.
func (db *DB) planAddNames(ctx context.Context, q querier, in AddNamesInput) (AddNamesResult, error) {
	res := AddNamesResult{Rows: []AddNamesRow{}}
	lines := strings.Split(strings.ReplaceAll(in.Names, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		row := parseNameLine(raw)
		row.Line = i + 1
		res.Rows = append(res.Rows, row)
	}
	if len(res.Rows) == 0 {
		return res, fmt.Errorf("%w: paste at least one name", ErrInvalid)
	}
	if len(res.Rows) > maxAddNames {
		return res, fmt.Errorf("%w: %d names at once is more than this is meant for; the limit is %d", ErrInvalid, len(res.Rows), maxAddNames)
	}

	taken, err := takenCodes(ctx, q)
	if err != nil {
		return res, err
	}

	// Numbers a line carries itself, checked first, so the range steps
	// round them.
	for i := range res.Rows {
		row := &res.Rows[i]
		if row.Error != "" || row.StudentNumber == "" {
			continue
		}
		sn, err := NormalizeStudentNumber(row.StudentNumber)
		switch {
		case err != nil:
			row.Error = strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
		case taken[strings.ToLower(sn)] != "":
			row.Error = sn + " " + taken[strings.ToLower(sn)]
		default:
			row.StudentNumber = sn
			taken[strings.ToLower(sn)] = "is on another line"
		}
	}

	var next func() (string, error)
	if first := strings.TrimSpace(in.FirstNumber); first != "" {
		next, err = numberRange(first, taken)
		if err != nil {
			return res, err
		}
	}
	for i := range res.Rows {
		row := &res.Rows[i]
		if row.Error != "" || row.StudentNumber != "" {
			continue
		}
		if next == nil {
			row.Error = "no student number; add one to the line, or give a first number to count from"
			continue
		}
		sn, err := next()
		if err != nil {
			return res, err
		}
		row.StudentNumber, row.Assigned = sn, true
	}
	for _, row := range res.Rows {
		if row.Error != "" {
			res.Failed++
		}
	}
	return res, nil
}

// takenCodes is every student number and serial in use, lowercased, with why.
// A number may equal neither (the trigger in CLAUDE.md §6).
func takenCodes(ctx context.Context, q querier) (map[string]string, error) {
	taken := map[string]string{}
	rows, err := q.Query(ctx, `
		select lower(student_number), coalesce(nullif(trim(coalesce(first_name, '') || ' ' || coalesce(last_name, '')), ''), full_name, 'an account')
		from profiles where student_number is not null
		union all
		select lower(serial_number), null from assets where serial_number is not null`)
	if err != nil {
		return nil, mapPgError("read student numbers", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var who *string
		if err := rows.Scan(&code, &who); err != nil {
			return nil, mapPgError("read student numbers", err)
		}
		if who != nil {
			taken[code] = "already belongs to " + *who
		} else {
			taken[code] = "is an item's serial"
		}
	}
	return taken, rows.Err()
}

// numberRange returns a generator of free numbers counting up from first.
// The trailing digits count and keep their width; anything before them is a
// fixed prefix. It skips anything taken and records what it hands out.
func numberRange(first string, taken map[string]string) (func() (string, error), error) {
	cut := len(first)
	for cut > 0 && first[cut-1] >= '0' && first[cut-1] <= '9' {
		cut--
	}
	prefix, digits := first[:cut], first[cut:]
	if digits == "" {
		return nil, fmt.Errorf("%w: the first number must end in digits, such as 900001 or G0001", ErrInvalid)
	}
	if _, err := NormalizeStudentNumber(first); err != nil {
		return nil, fmt.Errorf("%w: %s doesn't fit the student-number rule in Settings: %s", ErrInvalid, first,
			strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is too long to count from", ErrInvalid, first)
	}
	width := len(digits)
	return func() (string, error) {
		for tries := 0; tries < 100000; tries++ {
			candidate := prefix + fmt.Sprintf("%0*d", width, n)
			n++
			if len(candidate) > MaxStudentNumberLength {
				break
			}
			if taken[strings.ToLower(candidate)] != "" {
				continue
			}
			if _, err := NormalizeStudentNumber(candidate); err != nil {
				return "", fmt.Errorf("%w: %s doesn't fit the student-number rule in Settings", ErrInvalid, candidate)
			}
			taken[strings.ToLower(candidate)] = "is on another line"
			return candidate, nil
		}
		return "", fmt.Errorf("%w: the range from %s ran out of free numbers", ErrInvalid, first)
	}, nil
}

// parseNameLine reads one pasted line. Fields are split by tabs when there
// are any (a paste from a spreadsheet), else by commas. A field with a digit
// in it is the student number, since names don't have digits. Then:
//
//   - two name fields split by a tab are first and last, as a spreadsheet
//     lays them out;
//   - two split by a comma are "Last, First", as a school export writes them;
//   - one field is split at its last space: "Mary Ann Lee" is Mary Ann, Lee.
func parseNameLine(raw string) AddNamesRow {
	sep := ","
	if strings.Contains(raw, "\t") {
		sep = "\t"
	}
	var names []string
	var row AddNamesRow
	for _, f := range strings.Split(raw, sep) {
		f = strings.Join(strings.Fields(f), " ")
		switch {
		case f == "":
		case strings.IndexFunc(f, unicode.IsDigit) >= 0:
			if row.StudentNumber != "" {
				row.Error = "two student numbers on one line"
			}
			row.StudentNumber = f
		default:
			names = append(names, f)
		}
	}
	switch {
	case len(names) == 0:
		row.Error = "no name"
	case len(names) == 1:
		if i := strings.LastIndex(names[0], " "); i > 0 {
			row.FirstName, row.LastName = names[0][:i], names[0][i+1:]
		} else {
			row.FirstName = names[0]
		}
	case len(names) == 2 && sep == ",":
		row.LastName, row.FirstName = names[0], names[1]
	case len(names) == 2:
		row.FirstName, row.LastName = names[0], names[1]
	default:
		row.Error = "more than two name columns; paste first name, last name and, if there is one, the number"
	}
	return row
}
