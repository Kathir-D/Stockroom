# Installing Stockroom on the machine it will live on

This is the production install: the closet PC, or any machine that is going to
run Stockroom day to day. It is **not** the development setup — that is
`README.md` and `./scripts/dev.sh`, and it stays exactly as it was.

The difference in one line: development runs the Supabase CLI's whole stack,
and an install runs **one plain Postgres container and one binary**.

> **macOS and Linux.** Windows is not covered yet. `dev.ps1` has never run on
> real Windows hardware, and an untested service installer is worse than an
> absent one. `TEMPLATE-TODO.md` Phase A carries it.

---

## What you end up with

```
~/Stockroom/
├── stockroom            the server: API, web UI and database migrations, one file
├── stockroom-run.sh     what the service actually starts
├── docker-compose.yml   one postgres:17 container, bound to 127.0.0.1 only
├── .env                 generated; holds the database password; mode 600
├── uploads/             item and profile photos
├── backups/             nightly archives
├── photo-backups/       the photo mirror
└── logs/server.log
```

Plus a service registered with the operating system, so it starts on its own
and restarts if it dies. That part is not optional: the nightly backup is a
goroutine inside the server, so *the server not running* and *the machine not
backing up* are the same event.

---

## Install

You need Docker, Go and Node **on the machine you install from**. Go and Node
are build tools only — nothing but Docker is needed to *run* Stockroom
afterwards.

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/install.sh
```

It will:

1. check Docker is installed **and running** (it waits, and starts Docker
   Desktop for you on macOS),
2. build the web UI and compile it into the server binary,
3. create `~/Stockroom` and write a `.env` with a freshly generated database
   password,
4. ask for a **failsafe admin** student number and password,
5. start Postgres, apply every migration, start the server,
6. register the service and wait until `/health` answers before it says it is
   done.

Useful flags: `--home /some/path`, `--no-service`, `--no-open`,
`--admin-number` / `--admin-password` for an unattended run.

### The failsafe admin

This is the way back in when everything else has gone wrong — a forgotten
password, a bad roster import, a database restored from empty. It is re-applied
on every server start, so it cannot be locked out, and it is the only account a
fresh install has. Skipping it is allowed; the server will tell you it has no
failsafe admin on every admin's sign-in until you set one.

### macOS: turn on automatic login

The service is a **LaunchAgent**, which starts at login rather than at boot.
That is not a preference: Docker Desktop for Mac only runs inside a user
session, so a boot-time daemon would start, find no Docker, and give up.

So set the machine to log in by itself — **System Settings → Users & Groups →
Automatic login** — or a power cut leaves it at the login window, with nobody
able to check out a camera and nothing backing up, and no message anywhere
saying so.

On Linux this does not apply: Docker is a system service and Stockroom is a
systemd unit that starts at boot.

---

## After it is running

Open `http://127.0.0.1:8080`. **The database is empty** — no categories, no
equipment, no accounts except the failsafe admin if you gave the installer one.
That is correct; it is the difference between an install and the development
seed, which never loads here.

A **setup guide** walks through the rest, and saves as it goes, so you can stop
and come back:

- **If you skipped the failsafe admin**, the page asks you to create the first
  admin account right there — what your ID numbers look like, your number,
  your name and a password. That form only exists while there are no accounts
  at all.
- **If you gave the installer a failsafe admin**, sign in as it and the guide
  opens by itself.

The guide then covers: the spare "safety net" account (written into `.env` for
you, so it survives the database being lost), lending rules, your equipment
(upload a category list and a spreadsheet, add batches of identical items, or
load a few obviously-fake examples to try it out first), your class list, and a
backup folder — finishing with **Run a backup now and show me it worked**. A
backup folder must be a **full path**; a relative one is refused rather than
quietly resolved against whatever directory the service started in.

Afterwards, **Admin → Assets → Print labels** makes a PDF of barcode stickers
at the exact size of the sheet you bought. Print it at 100%, never "fit to
page". Anything that already carries a unique barcode from its manufacturer can
use that as its serial and needs no sticker. **Admin → Users → Print ID cards**
does the same for student cards, for schools whose cards carry no barcode.

The files in [`examples/`](../examples/) show every upload's shape.

Skipping the guide is fine: **Admin → Settings → Run the setup guide again**
brings it back, and everything it does is also on the ordinary admin screens.

---

## Upgrading

Pull the new code and run the same script:

```bash
cd Stockroom && git pull
./scripts/install.sh
```

It notices the existing install, **takes a database dump first**, rebuilds,
swaps the binary and restarts the service. The server applies any new
migrations at start-up. Your `.env` is never overwritten — backup settings live
in the database now, not in that file.

If the dump fails, the upgrade stops rather than continuing. That is the only
protection against a migration that goes wrong, and an upgrade that skips it
silently is not a trade anybody would agree to if asked.

---

## What happens when the power goes out

Nothing you have to do. The database container has `restart: unless-stopped`, so Docker brings it
back when the machine does; the service starts the server; and the server waits up to two minutes
for the database, because Docker Desktop takes about a minute to be ready after a boot and the
server would otherwise ask too early.

**Committed data survives an abrupt shutdown.** Postgres writes a write-ahead log before it
acknowledges anything, and replays it on the next start. This was tested rather than assumed: the
database container was killed outright, with no clean shutdown at all, immediately after an item was
added — and after the restart the item was there, with the recovery in the log. What you can lose is
a transaction that had not finished, which in practice means a checkout that was mid-click.

The one thing you must get right is **automatic login on a Mac** (above). Everything else recovers
on its own; a machine sitting at a login window does not.

## When something is wrong

```bash
tail -50 ~/Stockroom/logs/server.log        # what the server last said
curl http://127.0.0.1:8080/health           # is it up and can it see the database
cd ~/Stockroom && docker compose ps         # is Postgres running
```

**macOS**

```bash
launchctl print gui/$(id -u)/com.stockroom.server   # service state
launchctl kickstart -k gui/$(id -u)/com.stockroom.server   # restart it
```

**Linux**

```bash
systemctl status stockroom.service
sudo systemctl restart stockroom.service
```

Re-running `./scripts/install.sh` is a repair as much as an upgrade, and is the
first thing to try.

---

## Why Docker

Stockroom stores everything in Postgres, and Postgres has to come from
somewhere. The alternatives were bundling Postgres binaries for each platform —
better for you, meaningfully more packaging work, and it makes us responsible
for your Postgres upgrades — or rewriting the whole data layer on SQLite, which
means every query and the loss of the pgTAP test suite. Docker is one
install, it is the same on macOS, Linux and Windows, and it keeps the database
out of the way of anything else on the machine.

## Why not the Supabase CLI, which the developers use

It brings Kong, GoTrue, PostgREST, Realtime, Storage and Studio. Stockroom uses
none of them — the Go server is the only database client (`CLAUDE.md` §4) — so
an install would run about a dozen containers for the benefit of one, and would
ask you to install the CLI as well as Docker.

The decisive reason is not the container count. It puts **Studio on this
machine, on port 54323, with no authentication**: full read and write on every
table, including the roster, for anyone who walks past and opens a browser.
