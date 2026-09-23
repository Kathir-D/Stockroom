# Security

## Reporting a vulnerability

Please don't open a public issue. Use GitHub's private reporting instead, under the repository's
**Security** tab, then **Report a vulnerability**. I'll reply within a week.

Include what you did, what happened, and the commit you were on. A proof of concept helps but isn't
required.

## What counts

Stockroom is built to run on one machine, on localhost, with no internet access needed for daily
use. It trusts whoever is standing at that machine more than a web app would. Keep that in mind
when deciding whether something is a bug.

Things I want to hear about:

- Any way to reach the API from another machine without changing `SERVER_ADDR`
- A student account doing something the permission table in `CLAUDE.md` §7 says only an admin can
- A student number, password hash, backup token or photo-wall folder id showing up in a response,
  a log line or a backup where the docs say it shouldn't
- A restore that accepts a tampered or mismatched archive
- Anything that lets a signed-out visitor write to the database, apart from creating the first
  admin on an empty install

Things that are by design:

- Scanning a student number signs that student in with no password. The barcode is the credential,
  so treat ID cards like keys
- Anyone signed in can see who currently holds an item (`CLAUDE.md` §7)
- Backups carry student numbers and bcrypt hashes. Keep backup targets private, or turn on
  encryption in Admin, then Settings

## Supported versions

Only the latest commit on `main` gets fixes.
