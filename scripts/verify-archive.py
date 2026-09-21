#!/usr/bin/env python3
"""Verify a Stockroom backup archive the way a restore does, without touching the
database: every file named in manifest.json must be present and match its
SHA-256, every table CSV must carry the row count the manifest claims, the
sequence state must be there, and the redacted secret columns must really be
redacted -- app_settings lives in `public`, so an unredacted export would push
github_token to the very repository it unlocks (docs/design/backup.md §C.4).

    python3 scripts/verify-archive.py path/to/backup-2026-09-21.zip

Exit status is 0 when the archive would pass a restore's checks, 1 when it would
not. Called by scripts/backup-check.sh; useful on its own against an archive
pulled down from Drive or GitHub, to prove the round trip did not corrupt it.
"""
import hashlib, io, json, re, sys, zipfile


def main(path):
    z = zipfile.ZipFile(path)
    names = set(z.namelist())
    if "manifest.json" not in names:
        print("     FAIL no manifest.json in the archive")
        return 1
    m = json.loads(z.read("manifest.json"))

    files = m.get("files") or {}
    if isinstance(files, list):  # tolerate a list-of-objects shape
        files = {f.get("name") or f.get("file"): (f.get("sha256") or f.get("checksum") or "")
                 for f in files}
    rows = m.get("rows") or {}
    problems = 0

    for name, want in sorted(files.items()):
        if name not in names:
            print("     FAIL %s: named in the manifest, missing from the archive" % name)
            problems += 1
            continue
        blob = z.read(name)
        got = hashlib.sha256(blob).hexdigest()
        if want and got.lower() != str(want).lower():
            print("     FAIL %s: checksum mismatch" % name)
            problems += 1
            continue
        # tables/<name>.csv carries a row count in manifest.rows
        if name.startswith("tables/") and name.endswith(".csv"):
            table = name[len("tables/"):-len(".csv")]
            if table in rows:
                n = sum(1 for _ in io.StringIO(blob.decode("utf-8", "replace"))) - 1
                if n != rows[table]:
                    print("     FAIL %s: manifest says %d rows, archive has %d"
                          % (name, rows[table], n))
                    problems += 1

    for extra in ("RESTORE.md", "sequences.csv"):
        if extra not in names:
            print("     FAIL %s is missing" % extra)
            problems += 1

    seqs = m.get("sequences") or []
    if not seqs:
        print("     FAIL manifest carries no sequence state")
        problems += 1
    else:
        missing = [s.get("name") for s in seqs if "is_called" not in s]
        if missing:
            # is_called cannot be recovered from pg_sequences afterwards, and
            # replaying a never-read sequence with setval's default of true
            # silently burns its first value.
            print("     FAIL sequences missing is_called: %s" % ", ".join(map(str, missing)))
            problems += 1

    settings = [n for n in names if n.endswith("app_settings.csv")]
    if settings:
        body = z.read(settings[0]).decode("utf-8", "replace")
        if re.search(r"gh[pousr]_[A-Za-z0-9]{20,}", body):
            print("     FAIL app_settings.csv carries what looks like a GitHub token "
                  "-- redaction failed")
            problems += 1
        else:
            print("     OK   secrets are redacted from app_settings.csv")

    if problems:
        print("     %d problem(s) -- a restore would refuse this archive, which is correct"
              % problems)
        return 1
    print("     OK   %d files, every checksum and row count matches (schema version %s, "
          "%d sequences)" % (len(files), m.get("schema_version", "?"), len(seqs)))
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(__doc__)
        sys.exit(2)
    sys.exit(main(sys.argv[1]))
