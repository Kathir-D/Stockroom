# Example data

Everything here is **obviously fake** — "Example" in every name, student
numbers starting `900`, serials starting `EXAMPLE-` — so that nobody mistakes a
demo row for real inventory after setup.

| File | Import it from | What it is |
|---|---|---|
| `categories.media-department.md` | Admin → Categories → Import | One media department's real category tree, as an example of the shape |
| `categories.theatre.md` | Admin → Categories → Import | A theatre department's, so the shape reads as a pattern |
| `assets.csv` | Admin → Assets → Import | Five units filed under the media-department tree. Import that tree first |
| `roster.csv` | Admin → Users → Import roster | Five students with no password; each sets one at their first scan |

The category files are Markdown: `##` is a Type, `###` a Category and `-` a
Model, and any other line is a note. The same importer also takes a
`type,category,model` CSV, or plain text indented with tabs or two spaces.

`assets.csv` columns: `serial_number` and `name` are required. `category` or
`model` names where a unit files (the model wins when both are given; either
is matched by name, ignoring case). `status` is `available` or `unavailable`.
Re-importing a serial that already exists **updates** that item, and the
import report names what was there before.

These files are also the fixtures `internal/stockroom/examples_test.go`
imports, so if one stops importing cleanly the build says so.
