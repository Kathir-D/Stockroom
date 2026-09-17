# Restoring this backup

This archive is a complete copy of the Stockroom database: every table as a
CSV, the sequence positions, and a manifest naming how many rows each file
should hold and what its SHA-256 digest is.

## The normal way (no typing)

1. Open Stockroom and sign in as an admin.
2. **Admin → Backup → Restore**.
3. Choose this `.zip` file, type `RESTORE` in the confirmation box, press
   **Restore**.

Every check below runs either way: the checksums are verified before anything
is touched, the row counts and every foreign key are re-checked inside the
transaction, and a failure at any point leaves the database exactly as it was.

## If Stockroom will not start, or the database has no accounts in it

A restored-from-nothing database has nobody to sign in as, so the admin panel
cannot be reached. That is what the command-line restore is for. From the
Stockroom folder, in Terminal (macOS) or PowerShell (Windows):

```
go run ./cmd/restore --yes path/to/backup-YYYY-MM-DD.zip
```

or, if you have the built binary:

```
stockroom-restore --yes path/to/backup-YYYY-MM-DD.zip
```

It reads the same `DATABASE_URL` the server does, from `.env`, and runs the
same restore the admin panel runs — the same checksum, row-count and
foreign-key checks. It needs no account, because anyone who can run it already
has the machine.

If the archive is encrypted (its name ends in `.zip.enc`), the passphrase saved
in the admin panel is used automatically — omit the flag and it just works, and
the secret stays out of your shell history and out of `ps`.

If this database has no saved passphrase (you are restoring onto a fresh
machine, say), pass it explicitly with `--passphrase '<the passphrase>'`. The
flag takes its value on the command line; there is no prompt.

## What is in here

| File | What it is |
|---|---|
| `tables/*.csv` | The raw database tables. These are what a restore loads. |
| `sequences.csv` | Where each auto-numbering sequence had got to. |
| `manifest.json` | Row counts, schema version, and a SHA-256 per file. |
| `inventory.csv` | Every item, in one readable row each. Open it in Excel. |
| `accounts.csv` | Every account. **This file is sensitive** — see below. |

## A note on `accounts.csv`

It holds student numbers, and a student number signs its owner in by scanning
their ID card with no password. Treat this archive like a list of passwords:
keep it in a private repository or an unshared Drive folder, and do not email
it around.
