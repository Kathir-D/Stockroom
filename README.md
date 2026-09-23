<h1 align="center">Stockroom</h1>

<p align="center">
  <strong>Barcode-driven equipment checkout for a school media department.</strong><br>
  Scan a student ID to sign in, scan a sticker to check gear in or out, and always know who has what.<br>
  Runs entirely on one machine, with no internet connection needed for daily use.
</p>

<p align="center">
  <a href="https://github.com/Kathir-D/Stockroom/actions/workflows/tests.yml"><img alt="tests" src="https://github.com/Kathir-D/Stockroom/actions/workflows/tests.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="license: AGPL-3.0" src="https://img.shields.io/badge/license-AGPL--3.0-blue.svg"></a>
  <img alt="Go 1.25" src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white">
  <img alt="Svelte 5" src="https://img.shields.io/badge/Svelte-5-FF3E00?logo=svelte&logoColor=white">
  <img alt="PostgreSQL" src="https://img.shields.io/badge/PostgreSQL-17-336791?logo=postgresql&logoColor=white">
</p>

---

## Who this page is for

There are two of you, and this document serves both in order.

**A teacher, technician or student setting Stockroom up for a department.** Start at
[What you need](#what-you-need) and read to the end of [Part 4](#part-4--backup-and-restore). You
do not need to understand the code. You do need a terminal for now — see the honesty note below.

**A developer working on Stockroom.** [Part 5](#part-5--for-developers) is yours; skim Parts 1–4
first, because they describe the behaviour the code has to keep.

> [!IMPORTANT]
> **Installing it on the machine it will live on is now [`docs/INSTALL.md`](docs/INSTALL.md)** —
> `./scripts/install.sh` on macOS or Linux. That gets you one plain Postgres container, one binary
> holding the API, the web UI and every database migration, and a service that restarts it if it
> dies. On Linux the service starts at boot; on macOS it is a LaunchAgent that starts when the
> configured user **logs in**, so the Mac has to be set to log in automatically. It is also the upgrade: run it again.
>
> **Honest status.** It still needs Go and Node *on the machine you install from*, because it
> builds rather than downloading a release binary, and there is no Windows installer yet — both
> tracked in [`TEMPLATE-TODO.md`](TEMPLATE-TODO.md) Phase A. Once it is running, a first-run wizard
> walks through the first admin, the category tree and the equipment, and it prints its own
> barcode labels and ID cards (Phase B). If you are evaluating Stockroom for a school with nobody
> technical, read Phase A first and decide whether to wait.
>
> Everything below this point is the **development** setup, which is a different thing: the
> Supabase CLI, `seed.sql`, Studio and `./scripts/dev.sh`. Do not follow it for an install.

---

## Contents

**Part 1 — Setting it up**
[What Stockroom is](#what-stockroom-is) ·
[What you need](#what-you-need) ·
[Install](#install) ·
[Your first thirty minutes](#your-first-thirty-minutes)

**Part 2 — Running it**
[The student loop](#the-student-loop) ·
[The admin panel](#the-admin-panel) ·
[Importing a roster](#importing-a-roster) ·
[Barcode scanners](#barcode-scanners) ·
[Kits](#kits)

**Part 3 — Configuration**
[`.env`](#env-the-file-you-edit-once) ·
[Settings in the app](#settings-that-live-in-the-app) ·
[Rules you cannot change yet](#rules-you-cannot-change-yet)

**Part 4 — [Backup and restore](#part-4--backup-and-restore)** · [Sign-in photo wall](#sign-in-photo-wall)

**Part 5 — [For developers](#part-5--for-developers)**
[Architecture](#architecture) ·
[Layout](#project-layout) ·
[Tests](#tests) ·
[HTTP API](#http-api) ·
[House rules](#house-rules)

[**Troubleshooting**](#troubleshooting) · [**FAQ**](#faq) · [**Project status**](#project-status) ·
[**Contributing**](#contributing) · [**License**](#license)

---

# Part 1 — Setting it up

## What Stockroom is

A school media department owns cameras, lenses, lights, microphones, tripods and a drawer full of
batteries and SD cards. Students borrow them. Historically that was a clipboard, and the clipboard
does not tell you who still has the 70–200mm.

Stockroom replaces the clipboard. Every physical item wears a barcode sticker encoding its serial
number; every student ID card already carries a number. Both go through the same USB scanner, and
the software decides what a scan means from the screen you are on:

- **On the sign-in screen**, a scan is a login. No password, no typing, no queue.
- **Anywhere else**, a scan is an item. If the item is checked out, it is checked back in on the
  spot. If it is available, its detail card opens so it can be added to a cart.

Everything runs on a single dedicated PC in the equipment closet. There is no cloud service, no
account to register, and nothing that breaks when the school's internet does. The only network
traffic Stockroom ever makes is an optional nightly backup push.

### What it does

**The checkout loop** — sign in by scanning an ID; a filterable catalogue (Type → Category → Model,
plus search); a cart that gathers several items and checks them all out in one transaction; a due
date chosen at checkout and capped at 7 days; check-in by scanning, with an optional damage note,
by anybody signed in; full custody history on every asset and every person; **kits**, a named bundle
that goes out whole and comes back unit by unit.

**Guard rails** — overdue borrowers are blocked from new checkouts, enforced in the API and not
merely greyed out, with a per-checkout admin override; the current holder of an item is visible to
everyone by name, but never by student number, because that number is a working password-free
login; sessions expire on idle; a failsafe admin account in `.env` is recreated on every server
start, so a broken screen can never lock you out.

**Administration** — asset records with photos and an `unavailable` status for broken or missing
gear; category-tree editing; user management, password resets and a roster CSV import; kit
building; live "who has what" and overdue lists; a backup system that configures itself from a
screen and restores from an upload or a date.

### What it deliberately does not do

Reservations and future bookings. Physical locations — "where is it" is answered by "who has it".
Free-form tags, saved filters, per-asset custom fields. LAN or multi-station access: the web app is
bound to localhost on purpose. Native mobile apps. Email or SMS reminders, which would need the
internet the rest of the system is built to live without.

Those are scope decisions with reasoning behind them, not missing work. See §2 of
[`CLAUDE.md`](CLAUDE.md).

---

## What you need

### Hardware

| Thing | Requirement | Notes |
|---|---|---|
| **A dedicated PC** | Anything from the last decade with 8 GB of RAM | It lives in the closet and stays on. Windows is the tested deployment target; macOS and Linux work |
| **A monitor and keyboard** | Readable standing up | Students use it standing at a counter |
| **A USB barcode scanner** | Plain **HID keyboard-wedge** | ~$30–50. If it needs vendor software, it is the wrong scanner. See [Barcode scanners](#barcode-scanners) |
| **A label printer or sheet labels** | Anything that prints Code 128 | Avery 5160 / L7160 sheets through an ordinary laser printer are fine |
| **Student ID cards with barcodes** | Optional | Without them, people sign in by typing a number and a password. Everything still works, just slower |

**The PC must not sleep.** The nightly backup is a goroutine inside the running server, so "the
server is running" and "backups happen" are the same fact. Set the power plan to never sleep and
never hibernate.

### Software

| Dependency | Why | Install |
|---|---|---|
| **Go** 1.25+ | The API server and the desktop app's Go host | <https://go.dev/dl/> |
| **Node.js** 22+ (with npm) | Frontend tooling for both apps | <https://nodejs.org/en/download> |
| **Docker Desktop** | Runs the local Supabase stack, which hosts Postgres | <https://www.docker.com/products/docker-desktop/> |
| **Supabase CLI** | Starts Postgres, applies migrations, runs the database tests | <https://supabase.com/docs/guides/cli/getting-started> |
| **Wails CLI** | Builds and runs the desktop app | *installed automatically by the script* |
| **`rclone`** | Only if you want Google Drive backups, or the sign-in photo wall (it reads its photographs from a Drive folder) | <https://rclone.org/downloads/> — see [`docs/BACKUP-SETUP.md`](docs/BACKUP-SETUP.md) |

You do not need to install Wails by hand — `dev.sh` runs `go install` for it if it is missing.

Developed on macOS, deployed on Windows. Both start scripts exist; **the PowerShell one has not yet
been exercised on real Windows hardware**, which is the largest known risk in a first install.

---

## Install

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/dev.sh
```

That single command:

1. checks your tools are present and installs the Wails CLI if it is missing;
2. creates a `.env` from `.env.example` if you do not have one;
3. probes Postgres, then starts Docker and the Supabase stack (applying every migration and the
   development seed on a fresh database);
4. installs the npm workspace and Go modules, and points `core.hooksPath` at the tracked git hooks;
5. starts the API server and **waits for `/health` to actually answer** before going further;
6. launches both frontends and opens the browser tabs once their URLs respond.

On Windows, run `scripts\dev.ps1` instead — right-click → *Run with PowerShell*, or
`powershell -ExecutionPolicy Bypass -File scripts\dev.ps1`. On macOS you can also double-click
`scripts/Start Stockroom.command` in Finder; the first time, Gatekeeper may need a right-click →
**Open** → **Open** to approve it.

Press **Ctrl+C** to stop everything. Your data is preserved between runs.

### Sign in

The development seed creates two accounts. Both can sign in by *scanning* their number, or by
typing it and entering the password `password`:

| Student number | Name | Role |
|---|---|---|
| `123456` | Admin Admin | admin |
| `234567` | Student Student | student |

> [!WARNING]
> These are development credentials for a localhost-only stack, and they are **published in this
> public repository**. A real deployment gets its accounts from the roster import and the `.env`
> failsafe admin — never from `seed.sql`. Before the machine is in service, delete both seeded
> accounts or reset their passwords. See [Your first thirty minutes](#your-first-thirty-minutes).

### Where things are

| Service | URL |
|---|---|
| Web app | <http://localhost:5173/> |
| Desktop app (Wails dev server, also viewable in a browser) | <http://localhost:34115/> |
| API health check | <http://127.0.0.1:8080/health> |
| Supabase Studio (raw table editor) | <http://127.0.0.1:54323/project/default> |

The desktop app also opens as its own native window. Either UI is complete — they render the same
code. Use the desktop app on the closet PC; the web app is handy for a second browser tab while
administering.

> [!IMPORTANT]
> **The web app's port is pinned to 5173** (`vite --strictPort`). The API server's CORS allow-list
> contains 5173 and nothing else, so letting Vite drift to 5174 would fail every preflight — and
> because a blocked preflight and a dead server raise the *same* error in the browser, the UI would
> report "cannot reach the Stockroom server" while the server was perfectly healthy. Vite refuses
> to start on a busy port instead, which says what is actually wrong.

### The `dev` script

There is one script. Everything you do to a working copy is a subcommand of it.

| Command | What it does |
|---|---|
| `./scripts/dev.sh` <br> `./scripts/dev.sh up` | The whole environment, start to finish |
| `./scripts/dev.sh deps` | npm workspace + Go modules + git hooks, nothing else |
| `./scripts/dev.sh test` | Every suite — the same thing the pre-commit hook and CI run |
| `./scripts/dev.sh stop` | Stop Supabase and anything still holding a Stockroom port |
| `./scripts/dev.sh status` | What is running right now, and what is not |
| `./scripts/dev.sh --help` | The above, from the script itself |

`up` accepts `--no-desktop`, `--no-web`, `--no-open` (skip the browser tabs) and `--ci` (install
from the lockfile exactly). In PowerShell they are `-NoDesktop`, `-NoWeb`, `-NoOpen`, `-Ci`.

**`up` is safe to re-run after a crash.** If the API server is already up and healthy it reuses it
rather than starting a second copy that cannot bind the port; if something holds the port but fails
the health check, it says so and stops instead of launching a UI at a broken backend. It tears down
only what it started — a Supabase stack that was already running when you invoked it is still
running after you press Ctrl+C. Each service tees its output to `.run-logs/` (gitignored), so a
crash three minutes in leaves something to read.

### Running the pieces by hand

<details>
<summary>Manual setup, if you would rather not use the script</summary>

```bash
cp .env.example .env        # first time only; see Part 3
./scripts/dev.sh deps       # npm workspace + Go modules + git hooks
supabase start              # Postgres :54322 and Studio :54323, migrations and seed applied
go run ./server             # the API; check: curl http://127.0.0.1:8080/health
npm run dev:web             # web app on :5173
cd desktop-app && wails dev # desktop app, native window
supabase stop               # when you are done; data is preserved
```

The Go module is at the repository root, so `go build ./...` from there builds the server, the
desktop app's Go side and `internal/stockroom` together.

</details>

> [!CAUTION]
> **Run every npm command from the repository root.** This is an npm workspace: `packages/ui`,
> `web-app` and `desktop-app/frontend` share one `package-lock.json` and one installed tree. Running
> `npm install` *inside* one of the apps gives that app its own copy of Svelte and Vite, and the
> failure is silent and baffling — components render, but their state stops updating.
>
> `dev.sh deps` detects and repairs that, and both `up` and `test` call it, so you rarely have to
> think about it. (A `node_modules/.vite` directory under an app is *not* the problem; that is just
> Vite's dependency cache.)

---

## Your first thirty minutes

The order matters. Each step depends on the one before it.

### 1. Set a failsafe admin, before anything else

Open `.env` and fill in two lines:

```ini
ADMIN_STUDENT_NUMBER=900001
ADMIN_PASSWORD=a-long-password-you-will-not-lose
```

Restart the server. That account is re-created with `is_admin = true` and that password **on every
single start**, which makes it the way back in when a screen breaks, when somebody deletes the last
admin, or when a restore leaves the database with no accounts at all.

It is best-effort by design: if those values are blank, malformed or rejected, the server logs a
warning and starts anyway, because a typo in a config file must never take the whole API down. The
cost of that decision is that **the account only exists if you filled them in**, and the backup
screen will warn you until you do.

> [!IMPORTANT]
> Do this before you have any real data. It is the recovery path for every other mistake on this
> page, including the ones you make while following it.

### 2. Get rid of the seeded accounts

Sign in as `900001`. In **Admin → Users**, delete `123456` and `234567`, or reset their passwords
to something private. Their credentials are printed in this README and in `seed.sql`, and anybody
who can reach the machine can read them.

A user cannot be deleted while they hold items — Stockroom refuses with a 409 rather than orphan
the custody trail. Check items in first.

### 3. Build the category tree

**Admin → Categories.** The tree is exactly three levels:

```
Type              Lenses
  Category          Zooms
    Model             Canon 70-200mm f/2.8
```

Every physical unit you add later points at one of these nodes — normally a Model, though an asset
may file at any node if a middle level does not make sense for it (see
[`docs/adr/0001`](docs/adr/0001-assets-file-under-any-category-node.md)).

Three things to know before you start typing:

- **Sibling order is yours, not alphabetical.** Each node has a `sort_order` among its siblings, so
  the browse screen shows your department's own ordering rather than an alphabetised one.
- **Names are unique across the entire table, not per parent.** You cannot have "Accessories" under
  both Lenses and Audio. Pick "Lens Accessories" and "Audio Accessories". This trips up everybody
  once.
- **A branch may stop short.** A Category with no Models yet is fine and shows as an empty branch.

[`examples/categories.media-department.md`](examples/categories.media-department.md) is one department's tree, as an example of the shape. Building a
tree of any size through the dialog is tedious, so **Admin → Categories → Import** takes a whole
tree at once: an indented outline, a Markdown file of headings and list items, or a
`type,category,model` CSV. Re-importing a corrected file only adds what is new.

### 4. Add your equipment

**Admin → Assets → New asset.** Per unit:

| Field | Rule |
|---|---|
| **Serial number** | **Required and unique.** This is the scan key and what goes on the sticker |
| Name | What a student would call it |
| Category | A node from the tree above |
| Photo | Optional. 10 MB max; `.jpg .jpeg .png .gif .webp` |
| Status | `available` by default; set `unavailable` for broken, missing or retired gear |

An **asset tag** (`AST-000123`) is generated automatically and shown to admins only. You never type
one. It exists because the original schema had it; the serial is the identifier anybody reads.

**Two identical cameras are two rows**, each with its own serial. For linear stock — batteries,
bags, SD cards — use model-prefixed serials so every physical unit still has its own scannable
identity: `T7IBAT-001`, `T7IBAT-002`, `SD-014`.

For hundreds of items, **Admin → Assets → Import CSV** takes a spreadsheet keyed on
`serial_number` (a serial you already have updates that item and says what it replaced), and
**Add several** makes N numbered units of one model — `T7B-001` to `T7B-040` — continuing the
series from the highest serial already in use.

### 5. Print and apply stickers

Each sticker encodes the asset's **serial number** as a Code 128 barcode. **Admin → Assets →
Print labels** makes a PDF sheet for whatever the list shows, in label sizes named by what they go
on; print it at 100%. Keep serials short: the small label fits about 9 characters on Letter and 7
on A4. A sticker that falls off can be replaced from the asset's **Barcode** button, which shows
one large enough to scan off the monitor. If your ID cards carry no barcode, **Admin → Users →
Print ID cards** makes a sheet of CR80 cards that do.

Practical advice paid for in reprints: matte labels, not glossy — a glossy label under the counter
lamp will not scan. Put them where a hand does not rub: the underside of a camera body, the barrel
end of a lens, the top of a battery. Laminate anything that goes outdoors.

### 6. Import the roster

**Admin → Users → Import roster.** A CSV with a header row:

```csv
first_name,last_name,student_number,photo_path
Ada,Lovelace,900123,photos/900123.jpg
Alan,Turing,900124,
```

`first_name`, `last_name` and `student_number` are required. `photo_path` is optional and points at
a file **on this machine** — absolute, or relative to the photo directory you choose in the upload
form. Matching photos are copied into `uploads/profiles/<student_number>.<ext>`.

The import **upserts by student number**: names are replaced, and the account's `is_admin` flag,
password and existing photo survive. Re-importing next term's roster is safe and is the intended
way to keep it current. A bad row is reported with its line number and the rest of the file still
lands; only a malformed file — no header, a required column missing — is rejected whole.

**Imported accounts have no password.** The first time such a person *scans* their card they are
prompted to set one before they can do anything else; typed login is impossible until then. That is
the intended onboarding, and it costs a student about eight seconds.

### 7. Promote your admins

**Admin → Users**, tick *admin* on the accounts that should have the panel. `is_admin` is the only
permission flag in the system — there are no other roles and no per-feature permissions.

### 8. Configure backups

**Admin → Settings.** At minimum set a **backup folder** — a *full* path, starting with `/` (or a
drive letter on Windows) — which makes the nightly run work with no further configuration. A path
that has lost its leading separator on the way out of a Finder window is refused rather than
resolved, because the alternative is a valid backup written somewhere nobody will ever look while
every screen reports success. Google Drive and GitHub are optional and independent — configure either,
both or neither. [`docs/BACKUP-SETUP.md`](docs/BACKUP-SETUP.md) walks it click by click, and
[Part 4](#part-4--backup-and-restore) explains what it does.

Do this on day one, not later. The system tells everybody who signs in when backups have gone
stale, which is helpful only if they were ever configured.

### 9. Test the whole loop before a student does

Scan a card. Scan an item. Check it out with a due date. Scan it back in. Add a damage note. Look at
**Admin → Active custody** and confirm it says what you expect. Then set one item's due date into
the past through Studio and confirm the overdue warning and the checkout block both fire.

Half an hour here saves a bad first week.

---

# Part 2 — Running it

## The student loop

1. **Scan your ID card.** You are signed in. (Type the number by hand instead and you will be asked
   for a password; scanning is the fast path on purpose.)
2. **Find what you need.** Filter down the category tree on the left, or search.
3. **Add to cart.** Click an item, read the detail card, add it. Nothing is checked out yet.
4. **Check out.** Pick a due date — at most 7 days out — and the whole cart moves to `checked_out`
   in one transaction. You are then offered a sign-out prompt, because the closet PC is shared.
5. **Bring it back.** Scan the item's sticker from any screen. It is checked in immediately, and you
   can add a damage note afterwards.

Some details that are deliberate rather than accidental:

- **Anybody signed in can return anything.** A student returning a friend's tripod is a good
  outcome, not a permissions problem.
- **A cart only ever holds things being borrowed.** Scanning a checked-out item checks it in
  immediately and never touches the cart; the two directions never mix.
- **The cart survives a page reload** and clears only on sign-out or the idle timeout, so an
  accidental refresh mid-shopping does not lose it.
- **The 7-day cap is an exact instant** — seven times twenty-four hours from the moment of the
  request, not "the end of the seventh day".
- **Anyone can see who currently holds an item**, by name, with their due date and whether they are
  overdue. They cannot see that person's student number, because a student number signs its owner
  in without a password. Past holders are admin-only.
- **An overdue borrower is blocked**, at sign-in in the UI and again in the API regardless of what
  the client sends. An admin can override it for a single checkout.
- **Sessions expire on idle**, ten minutes by default, measured from the last interaction rather
  than from sign-in.

### First sign-in for a new account

Accounts created by the roster import, or by an admin in the panel, start with **no password**. The
first time such a person *scans* their card, they are asked to set one before they can do anything
else — until then, typed login is impossible. Admins can also set a password directly from the
panel. Passwords are 8 to 72 characters everywhere they are set.

## The admin panel

Admins get everything a student gets, plus:

| Screen | What it is for |
|---|---|
| **Assets** | Create, edit, delete, photograph, and mark `unavailable` |
| **Categories** | The three-level tree and its sibling ordering |
| **Users** | Create, edit, delete, promote to admin, reset passwords, import a roster |
| **Overdue** | Everything past its due date, with who has it |
| **Active custody** | Everything currently out, across everybody |
| **Kits** | Build bundles: create, rename, delete, add and remove units |
| **Backup** | Run one now, read the status of every target, restore from a date or an upload |
| **Settings** | Backup folders, schedule, retention, Drive, GitHub, encryption |

Two admin-only powers worth knowing: **checkout on behalf of somebody else** (pick the custodian
from the user list — for when a student is not at the machine), and the **overdue override** on a
single checkout.

For bulk edits or anything the panel does not cover, **Supabase Studio**
(<http://127.0.0.1:54323/project/default> → Table Editor) is a full Postgres table editor over the
same database. It has no guard rails; back up first.

## Importing a roster

Covered in [step 6](#6-import-the-roster) above. The short version: header row,
`first_name,last_name,student_number`, optional `photo_path`, upsert by student number, safe to
re-run each term.

Most student information systems will export something close. Open it in a spreadsheet, rename the
columns to match, delete the ones you do not need, save as CSV. Student numbers must be **digits
only**, 1 to 32 of them — if your school issues IDs with letters, Stockroom will not accept them
today (see [Rules you cannot change yet](#rules-you-cannot-change-yet)).

## Barcode scanners

Stockroom expects a plain **USB HID keyboard-wedge** scanner: it types the code into whatever has
focus and presses Enter. No SDK, no driver, no vendor software. If a scanner needs its own
application to work, it is the wrong scanner.

Two kinds of barcode flow through the same keyboard buffer:

| Barcode | Contents | Meaning |
|---|---|---|
| Student ID card | The student number | Sign in |
| Item sticker | `assets.serial_number` | Check in, or open the detail card |

Which one a scan means is decided by the active screen, never guessed from the payload. The backend
is told which it is.

**Telling a scan from typing** is a matter of speed: a scanner emits keystrokes a few milliseconds
apart, a person does not. The threshold lives in one place — `SCAN_KEY_THRESHOLD_MS` in
`packages/ui/src/lib/scanner.ts`, currently 50 ms — so tuning it against real hardware is a one-line
change. Press **Ctrl+Shift+D** anywhere in the app to print the last burst's inter-key timings to
the browser console; that is how you find the right number for the scanner you bought.

The keystroke buffer also watches for editing: Backspace moves it, and any caret key or editing
chord (`Alt/Cmd+Backspace`, `Cmd+A` then overtyping, paste) discards the pending burst entirely.
The sign-in screen goes further and only accepts a burst as a *scan* when the field on screen holds
exactly what the buffer saw. This is not fussiness — a scan signs somebody in with no password, so
a buffer that has drifted from the visible field is the difference between "nothing happened" and
"the wrong student was signed in".

> [!NOTE]
> No scanner has been bought yet, so the 50 ms default is a reasoned guess rather than a measured
> one. Budget twenty minutes with Ctrl+Shift+D on the day the hardware arrives.

## Kits

A kit is a named bundle — "Kit #1 = this camera, this lens, this bag" — that goes out as one thing.
Anyone can see kits and take one out; only admins build them.

The rules, and why they are asymmetric:

- **A kit holds no custody of its own.** Adding one to the cart expands it into its units, and
  checkout commits them like any other cart: one custody event per asset. That is why a kit
  checkout obeys the 7-day cap, the overdue block and the custodian rule automatically — there is
  no second code path to drift.
- **An asset belongs to at most one kit.** A unit shared between two bundles makes the second
  quietly incomplete, and that failure surfaces at the shelf, to a student who cannot fix it.
  Adding a unit that is already in a kit is refused, and the message names the kit that has it.
- **Kit names are unique, ignoring case.** It is a label on a bag; "Kit #1" and "kit #1" are the
  same bag.
- **Added whole, returned per unit.** A kit cannot be added to the cart unless every unit is on the
  shelf, because half a kit is a camera with no lens and you find out at the shoot. But a *return*
  processes each unit separately and reports `returned`, `already in` and `failed` — the units are
  physically on the counter, and refusing all four because one came back yesterday would leave the
  database claiming somebody still holds things they returned.
- **Deleting a kit is allowed even while its units are out**, unlike deleting an asset. Only the
  grouping disappears; no status and no custody row moves.

---

# Part 3 — Configuration

## `.env`, the file you edit once

Copy `.env.example` to `.env` at the repository root.

| Variable | Purpose | Default |
|---|---|---|
| `DATABASE_URL` | Direct Postgres connection | `postgresql://postgres:postgres@127.0.0.1:54322/postgres` |
| `SERVER_ADDR` | API listen address | `127.0.0.1:8080` |
| `ADMIN_STUDENT_NUMBER` | Failsafe admin account; 1–32 characters in the configured student-number format, digits by default | *(none)* |
| `ADMIN_PASSWORD` | Failsafe admin password; 8–72 characters | *(none)* |
| `UPLOADS_DIR` | Where profile and asset photos are copied | `./uploads` |
| `SESSION_IDLE_MINUTES` | Idle timeout, measured from the last interaction | `10` |
| `BACKUP_DIR` | **First-boot seed only** for the backup folder | *(none)* |
| `PHOTO_BACKUP_DIR` | First-boot seed only for the photo mirror folder | *(none)* |
| `RCLONE_REMOTE` | First-boot seed only for the Google Drive remote name | *(none)* |
| `SIGNIN_PHOTOS_REMOTE` | The rclone remote holding the photo wall's pictures. **Blank switches the whole feature off** | *(none)* |
| `SIGNIN_PHOTOS_FOLDER_ID` | First-boot seed only for the Drive folder; Admin → Photo wall owns it afterwards | *(none)* |
| `SIGNIN_PHOTOS_DIR` | Tile cache. **Wiped on every start**, so give it a directory of its own | `./.cache/signin-photos` |
| `SIGNIN_PHOTOS_COUNT` / `_BATCH` / `_TTL_MINUTES` | Tiles buffered, tiles per request, minutes before a shown tile is deleted | `48` / `16` / `15` |
| `SIGNIN_PHOTOS_MANIFEST_HOURS` | How often the Drive folder is re-listed | `168` (weekly) |

**The three backup variables are a seed, not a setting.** They fill the `app_settings` row the
first time the server starts against a fresh database, and are ignored on every start after that.
Backup configuration lives in Admin → Settings so that nobody has to edit a file; if `.env` won on
every boot, a value an admin typed would silently revert overnight.

**`SERVER_ADDR` is load-bearing.** The frontends hardcode `http://127.0.0.1:8080`, so pointing the
server anywhere else silently breaks them. `dev.sh up` warns when it notices.

**The sign-in photo wall** is entirely optional decoration: two slowly scrolling columns of the
department's own photography behind the sign-in card, pulled from a Google Drive folder through the
same `rclone` the backup uses. Leaving `SIGNIN_PHOTOS_REMOTE` blank disables it completely — no
goroutine starts and the screen is exactly what it would otherwise be.

## Settings that live in the app

Everything about backups is configured in **Admin → Settings**, not in a file: the backup folder,
the photo-mirror folder, the schedule hour, how long to keep runs, how stale is too stale, both
off-site targets, and the optional archive passphrase. Each card saves on its own, so a half-typed
field in one card cannot damage another.

The two secrets — the GitHub token and the archive passphrase — come back from the API **blank**,
with a flag saying whether one is set. They are never masked and never re-displayed, because a mask
eventually gets written back as a literal password. Leaving a secret field empty means "leave it
alone"; removing one is its own button.

## Rules you cannot change yet

These are hardcoded today. Every one of them is a school-policy decision that another department
would make differently, and each is tracked in [`TEMPLATE-TODO.md`](TEMPLATE-TODO.md) Phase T4.

| Rule | Current value | Where |
|---|---|---|
| Maximum checkout length | 7 days exactly | `internal/stockroom/custody.go`, `packages/ui/src/lib/due.ts` |
| Student number format | digits only, 1–32 | `internal/stockroom/password.go` |
| Category tree depth | exactly 3 levels | `internal/stockroom/categories_admin.go` |
| Overdue blocks checkout | always on, admin-overridable | `internal/stockroom/custody.go` |
| The word "student" | throughout the UI | `packages/ui` |
| Scan-vs-typed threshold | 50 ms | `packages/ui/src/lib/scanner.ts` |
| The name "Stockroom" | sign-in screen and both window titles | `screens/sign-in.svelte`, `index.html` |

If one of these blocks you, say so in an issue — the value of knowing which of them a real school
actually hits is higher than the cost of making it configurable.

---

# Part 4 — Backup and restore

Stockroom backs itself up. A goroutine inside the API server fires at the configured hour, and also
runs immediately on boot when the last successful run is stale — which is the whole of "back up
first thing when the machine is available" on a closet PC that gets unplugged. There is no Task
Scheduler entry and no launchd plist to go missing.

**[`docs/BACKUP-SETUP.md`](docs/BACKUP-SETUP.md) is the click-by-click setup**, written for whoever
is standing at the machine. The short version:

| Where | What goes there | Needs |
|---|---|---|
| This machine | A dated folder plus a restorable zip, pruned after `keep_days` | A folder path |
| Google Drive | The same folders, pushed with `rclone copy` | `rclone` installed, one Google sign-in |
| GitHub | One `backup/` path overwritten nightly, so git history *is* the backup list | A **private** repo and a fine-grained token |

Both off-site targets run when both are configured, and a push that fails never fails the run: the
restorable archive is already on disk, and one target being blocked says nothing about the other.

**What a backup contains.** Every table as CSV in one consistent snapshot, a `sequences.csv`, a
readable `inventory.csv` and `accounts.csv`, a `manifest.json` with a SHA-256 per file, and an
embedded `RESTORE.md` so the instructions travel with the archive. Optionally the whole thing is
encrypted with AES-256-GCM under a passphrase you hold.

> [!WARNING]
> **A backup archive is sensitive.** `accounts.csv` carries every student's name, number and
> password hash — and a student number is a working password-free login. If you push to GitHub, the
> repository **must** be private. If you push to Drive, the folder must not be shared. Encrypting
> the archive is the stronger answer, at the cost that a lost passphrase is an unrecoverable
> backup. This trade-off is argued out in [`docs/design/backup.md`](docs/design/backup.md) §C.5.

**Restoring is a feature, not a script.** Admin → Backup → pick a date from any target, or upload a
zip, then type `RESTORE`. It validates before it commits — per-file SHA-256 checksums before the
transaction even opens, then row counts against the manifest, then every foreign key re-checked by
anti-join, then every sequence proven to resume above its column's maximum. A restore that fails
leaves the database exactly as it was.

`cmd/restore` is the guaranteed floor beneath all of that: the same code, no session required, for
the case the panel cannot cover — a wiped database with no account left to sign in as.

```
go run ./cmd/restore --list
go run ./cmd/restore --yes path/to/backup-2026-09-16.zip
go run ./cmd/restore --yes --passphrase 'the passphrase' backup-2026-09-16.zip.enc
```

**Photos are mirrored locally only** — one live folder updated in place, rolling to a frozen
generation every `keep_days`, never deleted automatically. The mirror therefore only grows, so a
free-space threshold and a generation count warn on the same surfaces staleness uses, and deleting
a generation is a button a person presses. The accepted cost is that a dead drive loses `uploads/`
and its mirror together ([`docs/design/backup.md`](docs/design/backup.md) §H).

**Nothing fails quietly.** A backup that has not succeeded in `stale_hours` puts a line on
everyone's screen at sign-in: admins are told where to go, students are told which admin to mention
it to, by name only. Admin → Backup carries the same warnings, a per-target table with the exact
error from the last failed attempt, and the tail of `backup.log`.

> [!NOTE]
> The local target is verified end to end, repeatedly, including a full truncate-and-restore against
> a live database. **Neither off-site target has been exercised against a real account yet** — no
> Google Drive has been connected and no GitHub repository created. Both are unit-tested and hand-
> read, not round-tripped. Test yours before you rely on it, with the *Test connection* button and
> then an actual restore.

---

## Sign-in photo wall

Two slowly scrolling columns of department photography behind the sign-in card, read from a Google
Drive folder. It is **decoration and nothing else**, and it is **off unless you turn it on** — with
`SIGNIN_PHOTOS_REMOTE` unset no cache is created, no goroutine starts, and the sign-in screen is
exactly what it is without this feature. Nothing about it can delay, block or break signing in:
every failure resolves to the same place, which is no columns.

[`docs/design/signin-photo-wall.html`](docs/design/signin-photo-wall.html) is the full design. Setup
is four steps and needs a Google account.

### 1. Install rclone

The same binary the nightly backup uses, so if you have already set up Drive backups you can skip
this and the next step and reuse that remote.

```bash
brew install rclone            # macOS
winget install Rclone.Rclone   # Windows
```

### 2. Connect a Google account

```bash
rclone config
```

- `n` for a new remote, name it `gdrive`
- storage: `drive`
- `client_id` / `client_secret`: leave both blank (but read the caveat below)
- scope: **`drive.readonly`** — read-only, so this machine can never modify or delete anything in
  the Drive
- **`root_folder_id`: leave BLANK.** This one matters. A remote pinned to one folder cannot read a
  different one, and choosing the folder from the admin panel is the whole point; the folder is
  supplied per command instead
- `service_account_file`: blank
- advanced config: `n`
- auto config: `y` — a browser opens, sign in to Google, approve read-only access

Check it worked:

```bash
rclone lsd gdrive:
```

> **A public folder does not skip this step.** It is tempting to assume that sharing the folder
> "anyone with the link" means no sign-in is needed. It does not: rclone needs the OAuth token to
> *construct* the Drive backend, before it makes any network call and therefore before the folder's
> sharing setting is ever looked at. Measured against rclone v1.75.1 — an unauthenticated remote
> fails with `failed to create oauth client: empty token found`, public folder or not. Sharing
> affects who else can see the photographs, not how this machine reads them.

> **Caveat, as of 2026:** rclone warns that its shared Google Drive `client_id` *"is being retired
> and will stop working during 2026"*. If you hit that, make your own client_id
> ([rclone's instructions](https://rclone.org/drive/#making-your-own-client-id)) and supply it at
> the `client_id` prompt above. This affects Drive **backups** the same way, since both go through
> the same binary and the same default credential.

### 3. Point Stockroom at the remote

Add one line to `.env` and restart the server:

```
SIGNIN_PHOTOS_REMOTE=gdrive
```

This is the only photo-wall value that stays in `.env`, because it is an install-time fact about
the machine. Everything else is in the admin panel.

### 4. Choose the folder

**Admin → Photo wall.** Give the folder a short name, paste its Drive share link, press
**Use this folder**. Every form the Share button produces is accepted:

```
https://drive.google.com/drive/folders/<ID>?usp=sharing
https://drive.google.com/drive/folders/<ID>
https://drive.google.com/drive/u/0/folders/<ID>
https://drive.google.com/open?id=<ID>
<ID>                                    ← a bare id, pasted from elsewhere
```

The folder has to be openable by the Google account from step 2 — either shared with it, or set to
anyone-with-the-link. **The link is checked against Drive before anything is saved**, so a typo or
an unshared folder is a message on the screen rather than a wall that quietly empties ten minutes
later. If the check fails, nothing is written and the folder in use stays live.

Subfolders are fine and are walked to any depth. JPEG, PNG and WebP are used; HEIC, video and
anything roughly portrait or panoramic is skipped, because the wall crops everything to one
landscape 3:2 tile.

### What to expect afterwards

The first listing of a large folder takes **minutes**, and the wall is empty until it finishes —
the screen reports `Rebuilding — 12,400 files listed so far` so you can tell that apart from
something being broken. After that the reel fills one photograph at a time over a few minutes,
deliberately slowly, so a decorative buffer never earns a rate limit on the same Google account the
nightly backup uses. The folder is re-listed about once a week; **Re-list this folder** does it now,
which is what you want after uploading a batch you want on the wall today.

### Why the link is never shown back to you

The paste field starts empty every time, including straight after you have set a folder, and no
screen or API response ever contains the folder id. **A Drive folder link is a key to the folder,
not a name for it** — a folder shared "anyone with the link" is readable by whoever holds the URL,
so echoing it into a panel on a machine that sits unattended in a closet would hand it to anyone who
walks past. It is the same rule the GitHub backup token and every password field already follow. The
id is redacted from the backup export for the same reason, and the audit log records the name you
typed rather than the link.

So the folder is identified on that screen by **the name you gave it** and by **the preview strip**,
which shows six tiles the wall is about to use. The strip is the real confirmation — "are the right
photographs showing?" — and it reveals nothing the sign-in screen does not already show to everyone
who walks up to the machine. Looking at it does not consume them.

### Turning it off

Blank `SIGNIN_PHOTOS_REMOTE` and restart. The cache directory is emptied on the next start and the
sign-in screen goes back to what it was. Tiles are never backed up or mirrored — they are
re-derivable from Drive and live for about fifteen minutes.

---

---

# Part 5 — For developers

## Architecture

Three processes, one machine:

```
┌──────────────────────┐   ┌──────────────────────┐
│  Wails desktop app   │   │  Web app (Vite)      │   Both render the same
│  native window       │   │  localhost:5173      │   <StockroomApp> component
└──────────┬───────────┘   └──────────┬───────────┘   from packages/ui
           │                          │
           └───────── fetch ──────────┘
                        │  JSON over HTTP, localhost only
                        ▼
           ┌────────────────────────────┐
           │  Go API server  :8080      │   server/          — HTTP handlers
           │                            │   internal/stockroom — ALL the logic
           └────────────┬───────────────┘
                        │  pgx, direct connection
                        ▼
           ┌────────────────────────────┐
           │  PostgreSQL  :54322        │   hosted by the Supabase CLI stack
           │  (Supabase local stack)    │   (Docker); Studio on :54323
           └────────────────────────────┘
```

Three properties explain most of the layout:

**The Go server is the only database client.** Every query, transaction and permission check lives
in `internal/stockroom`. The frontends hold no SQL, no Supabase client and no credentials — they
call HTTP endpoints and render what comes back. A rule enforced in the UI only is a rule that a
`curl` command ignores, so authorisation is checked inside the package, below the HTTP layer.

**Supabase is used as a Postgres distribution, nothing more.** Migrations, seeding and the Studio
table editor are genuinely convenient; its REST API and Auth service are unused, and row-level
security is off because nothing but the Go server ever connects.

**The frontend exists once.** `packages/ui` (`@stockroom/ui`) holds every component, screen, store
and the API client. `desktop-app/frontend/src/App.svelte` and `web-app/src/App.svelte` each render
`<StockroomApp>` and contain nothing else, so the two UIs cannot drift apart.

## Project layout

```
stockroom/
├── internal/stockroom/     ALL business logic; the only code that touches Postgres
│   ├── db.go               the pool, the session store, the configured directories
│   ├── auth.go             Actor, RequireAdmin, RequireFullSession, login/logout
│   ├── users.go roster.go  user CRUD, password resets, roster CSV import
│   ├── categories*.go      the category tree, and its admin CRUD
│   ├── assets*.go          browse and admin asset operations
│   ├── custody.go          ScanItem, CheckOutAssets, CheckInAsset, histories
│   ├── kits.go             kits: CRUD, membership, CheckInKit (a kit holds no custody)
│   ├── backup*.go archive.go restore.go   the nightly run, the archive, the one restore
│   ├── target*.go          Google Drive via rclone; GitHub via the REST API
│   └── photowall*.go       the optional sign-in photo reel
├── server/                 net/http JSON API; handlers decode, call the package, encode
├── cmd/restore/            the session-free disaster CLI, same RestoreFromZip
├── packages/ui/            @stockroom/ui — every component, screen, store and the API client
│   └── src/lib/
│       ├── app.svelte      the whole application: routing, the scan listener, the 401 hook
│       ├── api/            one function per endpoint, over one fetch client
│       ├── scanner.ts      keystroke buffer, scan-vs-typed, the Ctrl+Shift+D diagnostic
│       ├── styles/tokens.css  the only file allowed to define a colour, radius or duration
│       ├── components/     shadcn-svelte primitives + Stockroom components
│       └── screens/        sign-in, browse, cart, kits, history, admin/*
├── desktop-app/            Wails host (Go side is a window, nothing more)
├── web-app/                Vite + Svelte 5 host
├── supabase/               config.toml, migrations/, seed.sql, tests/ (pgTAP)
├── scripts/                dev.sh, dev.ps1, Start Stockroom.command
├── docs/                   adr/ · design/ · agents/ · BACKUP-SETUP.md
└── uploads/                photos, served at /files/ (gitignored)
```

## Tests

```bash
./scripts/dev.sh test
```

Runs `go vet`, the Go tests, pgTAP against the local Postgres, `svelte-check`, Vitest and both
frontend builds — the same steps CI runs, in the same order. Go packages run with `-p 1`, because
they share one live database and the restore tests truncate every table in it.

The database suites need the Supabase stack running. If Postgres is down, the Go tests that need it
and the pgTAP run are reported as **FAILED**, not skipped, on purpose: a silent skip that looks like
a pass is worse than a red run.

The suite is deliberately small — one happy path and one permission gate per module.
[`TESTING.md`](TESTING.md) lists what runs, what was cut and what is worth adding next.

### Pre-commit hook

`.githooks/pre-commit` runs the full suite before a commit is created, so a red build is caught on
the machine that broke it. `dev.sh deps` installs it by pointing `core.hooksPath` at the tracked
`.githooks/` directory, which is why the hook lives in the repository rather than in `.git/hooks`: a
hook nobody can see is a hook nobody maintains.

Docs-only commits skip it automatically, using the same path list as the CI workflow. `git commit
--no-verify` and `STOCKROOM_SKIP_TESTS=1` are the deliberate escapes; CI runs the same suite on the
pull request, so skipping the hook delays a failure rather than hiding it.

### Database changes

Migrations live in `supabase/migrations/` and are applied automatically by `supabase start` and
`supabase db reset`. `supabase/seed.sql` loads on reset and builds a 63-node category tree, twelve
assets, a kit and two accounts, for development only.

`supabase db reset` reapplies every migration and reloads the seed. **It destroys all local data.**

## HTTP API

All routes are JSON over `http://127.0.0.1:8080`, localhost only. Authentication is a session token
returned by either login route and sent as `Authorization: Bearer <token>` or as the HttpOnly
`stockroom_session` cookie. "Admin" below is enforced inside `internal/stockroom`, not by the
router.

In the **Who** column: *anyone* needs no session · *any session* includes a password-less "limited"
one · **any full** is every signed-in user whose password is set — a limited session gets `403
{"error":"password not set","needs_password":true}` from these.

| Route | Who | Notes |
|---|---|---|
| `GET /health` | anyone | Pings Postgres |
| `POST /auth/scan` · `POST /auth/password` | anyone | `{student_number}` / `{student_number, password}` |
| `POST /auth/set-password` · `POST /auth/logout` · `GET /me` | any session | Reachable from a password-less "limited" session |
| `GET /categories/tree` | any full | Nested Type → Category → Model, in `sort_order` |
| `GET /assets?category=&status=&q=` · `GET /assets/{id}` | any full | Rows carry `category_path`, `photo_url` and the current holder |
| `POST /scan` | any full | `{serial}` — checked out ⇒ checked in; available ⇒ detail |
| `POST /checkout` | any full | `{asset_ids, due_at, custodian_id?, override_overdue?}`; the last two are admin-only |
| `POST /assets/{id}/checkin` | any full | Optional `{note}` |
| `POST /custody/{id}/note` | any full | Adds a damage note to a *closed* custody event |
| `GET /kits` · `GET /kits/{id}` | any full | Units as browse rows, plus counts and `checkable` |
| `POST /kits` · `PUT/DELETE /kits/{id}` | admin | A new kit is empty; deleting removes only the grouping |
| `POST /kits/{id}/items` · `DELETE /kits/{id}/items/{assetId}` | admin | A unit already in a kit is 409 **naming that kit** |
| `POST /kits/{id}/checkin` | any full | Per unit: `returned`, `already_in`, `failed` |
| `GET /users/{id}/history` | self or admin | |
| `GET /custody/active` · `GET /custody/overdue` · `GET /assets/{id}/history` | admin | |
| `GET/POST /users` · `GET/PUT/DELETE /users/{id}` · `POST /users/{id}/password` | admin | |
| `POST /users/import` | admin | Roster CSV, multipart or `text/csv` |
| `POST /assets` · `PUT/DELETE /assets/{id}` · `POST /assets/{id}/status` | admin | `serial_number` required; `asset_tag` is generated |
| `POST /assets/{id}/photo` | admin | Multipart, 10 MB, `.jpg .jpeg .png .gif .webp` |
| `POST /categories` · `PUT/DELETE /categories/{id}` | admin | Depth capped at 3 |
| `POST /categories/import` · `POST /assets/import` | admin | A category tree (outline, Markdown or CSV) and an asset CSV upserting by serial |
| `POST /assets/bulk-preview` · `POST /assets/bulk` | admin | N numbered units of one model; the preview writes nothing |
| `GET /labels/layouts` · `POST /assets/labels.pdf` · `POST /users/cards.pdf` | any full · admin · admin | Label sheets and ID cards as PDFs |
| `GET /assets/{id}/barcode.png` | any full | One barcode, for a sticker that fell off |
| `POST /admin/backup` | admin | Runs a backup now: CSV per table, sequences, a manifest, then every enabled target |
| `GET/PUT /admin/settings` · `POST /admin/settings/test` | admin | Backup configuration, and a connection check against one target |
| `POST /admin/drive/connect` · `POST /admin/drive/finish` | admin | Connects a Google account through `rclone authorize`, no terminal |
| `GET /admin/backup/status` · `GET /admin/backup/versions` | admin | Per-target state and the dated backups available |
| `POST /admin/restore` · `POST /admin/restore/remote` | admin | Restore from an uploaded archive, or one picked by date |
| `GET /admin/photos/generations` · `POST /admin/photos/restore` · `DELETE /admin/photos/generations/{name}` | admin | The local photo mirror |
| `GET /admin/photo-wall` · `PUT /admin/photo-wall` · `POST /admin/photo-wall/rebuild` · `GET /admin/photo-wall/preview` | admin | The sign-in wall's Drive folder. `PUT` takes `{link, label}` and probes Drive before it writes. **No response ever contains the folder id or a Drive URL** |
| `GET /files/...` | anyone | Photos; `<img>` tags cannot send a bearer token |
| `GET /signin/photos` · `GET /signin-photos/...` | anyone | The optional sign-in photo wall. Always 200, `[]` when off |

Error mapping: `404` not found · `400` invalid · `401` unauthorised or bad credentials · `403`
forbidden · `409` conflict or overdue-blocked · **`503` not configured** (an unset `UPLOADS_DIR` or
an unset backup folder, with the message passed through — that is neither the client's fault nor a
bug, and the admin reading it is the person who fixes it on the Settings screen) · `500` for
everything else, with the detail logged rather than sent.

Full table with request and response shapes: [`CLAUDE.md`](CLAUDE.md) §8.1.

## House rules

Conventions the code assumes and reviews enforce:

- **Permission checks belong in `internal/stockroom`**, not in `server/` and certainly not in the
  UI. The router is a transport; it is not the security boundary.
- **One writer per `photo_path` column.** `SetAssetPhoto` for assets, the roster import for
  profiles. A form field that can write the column can also point it at a file nobody uploaded.
- **Design tokens live in `packages/ui/src/lib/styles/tokens.css`.** It is the only file allowed to
  define a colour, radius, shadow or duration. No component CSS files, no `tailwind.config.js`.
- **Every decision that cost an argument gets a line in `CLAUDE.md` §13**, with the reasoning and
  the alternative that lost. The log is the point of the file.

## Further reading

| Document | What it covers |
|---|---|
| [`CLAUDE.md`](CLAUDE.md) | The master reference: architecture, schema, permissions, endpoint table, and a decision log with the reasoning behind each choice |
| [`CONTEXT.md`](CONTEXT.md) | Domain glossary — the vocabulary the code and the docs both use |
| [`TODO.md`](TODO.md) | Phase-by-phase build plan for the software itself |
| [`TEMPLATE-TODO.md`](TEMPLATE-TODO.md) | **Turning this into something another school can install**, and the GitHub setup for it |
| [`TESTING.md`](TESTING.md) | What is tested, what was deliberately cut, and why |
| [`CI.md`](CI.md) | The GitHub Actions job, branch protection, the pre-commit hook |
| [`docs/BACKUP-SETUP.md`](docs/BACKUP-SETUP.md) | Click-by-click backup setup for a non-technical admin |
| [`docs/design/design-system.md`](docs/design/design-system.md) | The UI: tokens, components, screen specs |
| [`docs/design/backup.md`](docs/design/backup.md) | The full backup and restore design and its reasoning |
| [`docs/adr/`](docs/adr/) | Architecture decision records |

---

## Troubleshooting

<details>
<summary><strong>The UI says it cannot reach the Stockroom server</strong></summary>

Check `./scripts/dev.sh status` first. A blocked CORS preflight and a dead server raise the same
error in the browser, so the message cannot tell them apart. The usual causes, in order: the API
server is not running; Docker or Supabase is down so `/health` fails; or the web app is on a port
other than 5173 and is failing the CORS allow-list.

</details>

<details>
<summary><strong>Components render but nothing updates when I interact</strong></summary>

That is the two-copies-of-Svelte failure, and it means a nested `node_modules` is shadowing the
workspace. Run `./scripts/dev.sh deps`, which detects and repairs it. Always install from the
repository root.

</details>

<details>
<summary><strong>Port 5173 is in use and Vite refuses to start</strong></summary>

Deliberate — see the note in [Where things are](#where-things-are). Free the port
(`./scripts/dev.sh stop`, or `lsof -ti:5173 | xargs kill`) rather than letting Vite pick another
one, or every request will fail the CORS preflight.

</details>

<details>
<summary><strong>"Something holds :8080 but does not answer /health"</strong></summary>

An API server is running without a working database — usually Docker or Supabase stopped underneath
it. `./scripts/dev.sh stop`, then `./scripts/dev.sh up`.

</details>

<details>
<summary><strong>A scan asks for a password instead of signing me in</strong></summary>

The burst was read as typed. Either the keystroke gaps exceeded `SCAN_KEY_THRESHOLD_MS` — press
Ctrl+Shift+D to see the actual timings and tune the constant — or the field on screen no longer
matched what the scanner sent, which happens if the field was edited mid-scan. Clear the field and
scan again.

</details>

<details>
<summary><strong>A scan does nothing at all</strong></summary>

Open a text editor and scan into it. If nothing appears, the scanner is not in keyboard-wedge mode
(check its manual for a "USB HID / keyboard" configuration barcode) or it is not powered. If the
code appears but without a newline, the scanner needs a "suffix: Enter" configuration barcode —
Stockroom keys off the Enter.

</details>

<details>
<summary><strong>A scanned item opens the wrong record, or none</strong></summary>

The sticker and the database disagree. Find the item by name in Admin → Assets and compare its
serial with what the scanner types into a text editor. Whitespace, a leading zero and a `0`/`O`
confusion are the usual three.

</details>

<details>
<summary><strong>I am locked out of the admin panel</strong></summary>

Set `ADMIN_STUDENT_NUMBER` and `ADMIN_PASSWORD` in `.env` and restart the server. That account is
recreated with that password on every start, by design, precisely for this.

</details>

<details>
<summary><strong>A student left with equipment and their account must go</strong></summary>

You cannot delete an account holding open custody — Stockroom refuses rather than orphan the trail.
Check the items in first (scan them, or use Admin → Active custody), then delete. If the gear is
genuinely gone, check it in and set those assets to `unavailable` with a note, which keeps the
history intact and takes them out of circulation.

</details>

<details>
<summary><strong>The backup screen says backups are stale</strong></summary>

Open Admin → Backup. The per-target table carries the exact error from the last failed attempt, and
the tail of `backup.log` is below it. The two most common causes are a backup folder that was never
configured (a 503, *not configured*) and an off-site token that expired.

</details>

<details>
<summary><strong>The photo wall's "Replace folder" button is greyed out</strong></summary>

This server has no Drive source, so a pasted link could not be checked and would not be saved. The
**Status** block above the form says which piece is missing — usually `rclone` is not installed, or
`SIGNIN_PHOTOS_REMOTE` is not in `.env`. Both need a server restart afterwards. See
[Sign-in photo wall](#sign-in-photo-wall).

</details>

<details>
<summary><strong>"not configured: the sign-in photo wall has no Drive source on this server"</strong></summary>

The same thing, from the API rather than the screen. It is a 503 on purpose: a setting nobody has
filled in yet is not a bug and not your fault. Work through
[Sign-in photo wall](#sign-in-photo-wall) — installing rclone alone is not enough, the remote has
to be named in `.env` and the server restarted.

</details>

<details>
<summary><strong>The folder saved, but the wall is still empty</strong></summary>

Normal for the first few minutes, and the admin screen distinguishes the cases. `Rebuilding — N
files listed so far` means the folder is still being listed, which takes minutes on a large one.
After that the reel fills roughly one photograph every couple of seconds, on purpose. If it stays
empty with a listing built, check the photo count: everything portrait, panoramic, HEIC or video is
skipped, so a folder of phone portraits legitimately yields nothing.

</details>

<details>
<summary><strong>I want to start over with a clean database</strong></summary>

`supabase db reset` reapplies every migration and reloads `seed.sql`. It destroys all local data.
Back up first if there is anything in there you want.

</details>

---

## FAQ

### Deciding whether to use it

<details>
<summary><strong>What does it cost to run?</strong></summary>

Nothing, in software. Stockroom is free, self-hosted and has no subscription, no per-seat cost and
no cloud account. You need a PC you probably already have, a USB scanner (~$30–50) and label
sheets. Google Drive and GitHub backups both fit comfortably in free tiers.

</details>

<details>
<summary><strong>How long does setup take?</strong></summary>

Installing the software: an hour or two today, mostly waiting on downloads, and longer if you have
never used a terminal. Entering your inventory: that depends entirely on how much gear you have and
is the real cost — budget an afternoon with a volunteer per hundred items, plus sticker printing.

</details>

<details>
<summary><strong>Do we need an internet connection?</strong></summary>

Not for anything daily. Sign-in, browsing, checkout, check-in, history and the admin panel are all
local. Two optional features use it, both through `rclone`: the nightly backup push, where a failed
push never interferes with the system working because the local archive is written first, and the
sign-in photo wall, which pulls its photographs from a Google Drive folder and simply draws nothing
when it cannot reach one. Leave both switched off and nothing in Stockroom touches the network.

</details>

<details>
<summary><strong>Can students use it from their phones, or from another computer?</strong></summary>

No, deliberately. The web app is bound to localhost on the closet PC and the API only accepts
loopback origins. Opening it to the LAN would mean authentication designed for a supervised counter
suddenly guarding a network service — scan login with no password is safe at a machine in a closet
and unsafe on a network anybody can reach. It is listed as a non-goal in `CLAUDE.md` §2.

</details>

<details>
<summary><strong>Is our student data safe? What about FERPA or GDPR?</strong></summary>

What Stockroom stores: names, student numbers, optional photographs, and a permanent record of who
borrowed what and when. Where it lives: one PC, plus whichever backup targets you enable. Who sees
it: any signed-in user can see the *current* holder of an item by name; admins see everything.

Two things to take to whoever handles data at your school. First, **there is no retention policy** —
custody history is kept forever unless someone deletes it, and deleting a user deletes their trail
with them. Second, **a backup archive is a roster of working credentials**, because a student
number signs its owner in without a password; if you push backups anywhere, that destination must be
private, and encrypting the archive is stronger.

Stockroom does not claim compliance with anything. It tells you exactly what it does so your data
officer can decide.

</details>

<details>
<summary><strong>Can it handle our whole school, not just one department?</strong></summary>

One installation is one department's inventory with one shared pool of accounts. There is no
concept of separate departments, and building one is not planned. Two departments would want two
installations on two machines.

</details>

<details>
<summary><strong>How many items and users can it handle?</strong></summary>

It is Postgres, so the database is not the limit. The browse list is sorted in the application
rather than in SQL, which is a deliberate choice for a catalogue of a few hundred units and would
want revisiting in the thousands. Nobody has load-tested it; a department-sized inventory is
comfortably within what it was designed for.

</details>

<details>
<summary><strong>Does it integrate with our student information system or Google Classroom?</strong></summary>

No. The bridge is a CSV: export your roster from the SIS, rename the columns, import it. That is
deliberate — an integration is a dependency on a vendor's API, a credential to maintain and an
internet connection, in a system built to work without all three.

</details>

<details>
<summary><strong>Can we reserve equipment for a future date?</strong></summary>

No. Reservations are an explicit non-goal. The database has unused `bookings` tables from an
earlier design, and nothing reads or writes them. The workflow Stockroom supports is "take it now,
bring it back by then".

</details>

<details>
<summary><strong>Is there a mobile app?</strong></summary>

No, and none is planned. The interaction is a person standing at a counter with a scanner.

</details>

<details>
<summary><strong>Who maintains this? What if we depend on it and it stops being developed?</strong></summary>

It is coursework, built on a nine-week schedule for a real department, by one student. Treat the
maintenance promise as best-effort. The mitigations that matter: it is open source, so you can fix
it; it runs on your hardware, so nobody can turn it off; and your data is in a plain Postgres
database with a nightly CSV export, so getting it out is never a rescue operation.

</details>

### Setting it up

<details>
<summary><strong>Do we really need Docker, Go and Node just to run it?</strong></summary>

Today, yes, and that is the honest weak point of the project. The plan to replace all of it with a
single downloadable binary plus one Postgres container is Phase T5 of
[`TEMPLATE-TODO.md`](TEMPLATE-TODO.md), and Phase T6 is the installer that then checks for Docker,
installs it if it is missing, survives the reboot Windows sometimes needs, and hands you a setup
wizard. Until those land, installation is a developer task.

</details>

<details>
<summary><strong>Windows or Mac?</strong></summary>

Windows is the intended deployment target — the closet PC. Development happens on macOS, and both
start scripts exist. The PowerShell script has **not** been run on real Windows hardware yet, so
budget time for surprises on a first Windows install, and please report what breaks.

</details>

<details>
<summary><strong>Our student IDs contain letters. Will it work?</strong></summary>

Not today. Student numbers must be digits, 1 to 32 of them, and the sign-in field filters out
anything else. This is the single most likely blocker for another school and it is the second item
in [`TEMPLATE-TODO.md`](TEMPLATE-TODO.md) Phase T4. If this is you, open an issue — it moves up the
list.

</details>

<details>
<summary><strong>Our ID cards have no barcode, or the barcode is not the student number.</strong></summary>

Everything still works: people type their number and a password instead. You lose the speed, not
the function. The alternative is printing your own cards with the number as Code 128, which is the
same generator the item stickers use.

</details>

<details>
<summary><strong>We have 300 items. Do I really add them one dialog at a time?</strong></summary>

No. **Admin → Assets → Import CSV** takes a spreadsheet, and **Add several** makes a numbered run
of one model. Remember every unit needs its own unique serial, including each identical battery.

</details>

<details>
<summary><strong>Where do the barcode stickers come from?</strong></summary>

Stockroom prints them: **Admin → Assets → Print labels** makes a PDF for Avery-style sheets. Print
at 100%. Use matte labels, and put them somewhere a hand does not rub.

</details>

<details>
<summary><strong>Can we change the name, the logo, the colours?</strong></summary>

The name and tagline are hardcoded in `packages/ui/src/lib/screens/sign-in.svelte` and both
`index.html` files — editable if you are comfortable with a text editor, not yet a setting. Colours
live in one file, `packages/ui/src/lib/styles/tokens.css`. Making the name and logo a setting is
Phase T3; a colour picker is deliberately *not* planned.

</details>

<details>
<summary><strong>How do we upgrade to a newer version?</strong></summary>

`git pull`, then `supabase db reset`… **no** — that would destroy your data. Today the honest
answer is: back up first, `git pull`, run `supabase migration up` (or restart the stack, which
applies pending migrations), and restart the server. There are no versioned releases and no
documented upgrade path yet; both are Phase T5/T9. Always take a backup before pulling.

</details>

### Daily operation

<details>
<summary><strong>Can a student check something out for a friend?</strong></summary>

No — a checkout is always to the person signed in, unless an **admin** does it and picks the
custodian from the user list. That keeps "who has it" answerable.

</details>

<details>
<summary><strong>Can anybody return anything?</strong></summary>

Yes, on purpose. Any signed-in person can scan any checked-out item and it is returned immediately.
A student bringing back a friend's tripod is a good outcome, and requiring the borrower to be
present would mean gear sitting in a bag instead of on the shelf.

</details>

<details>
<summary><strong>What happens if somebody forgets to sign out?</strong></summary>

The session expires after ten minutes of no interaction, measured from the last thing they did, and
the cart clears with it. After a checkout completes, the UI also offers a sign-out prompt, because
the machine is shared.

</details>

<details>
<summary><strong>Why can students see who has an item?</strong></summary>

Because a student who wants the 70–200mm is better served by "Ada has it until Friday" than by
silence, and there is no privacy officer for this project to have sought a different answer from.
They see the holder's **name**, due date and overdue state — never the student number, because that
number is a working login. Past holders are admin-only. This was decided, reversed, and decided
again; the reasoning is in `CLAUDE.md` §13 (2026-09-12).

</details>

<details>
<summary><strong>A student is overdue and needs something urgently.</strong></summary>

An admin can override the block for a single checkout at the checkout screen. The override is per
checkout, not a flag on the account, so it cannot be forgotten.

</details>

<details>
<summary><strong>Something came back broken. Where does that go?</strong></summary>

Scan it in, then add a damage note — the note lands on that custody event, so it is attached to the
person and the day. If it is unusable, set the asset to `unavailable` in the admin panel, which
takes it out of circulation without deleting anything.

</details>

<details>
<summary><strong>An item is lost. Do I delete it?</strong></summary>

Prefer `unavailable`. Deleting an asset takes its entire custody history with it, and the history is
the thing that tells you it was lost and who had it. Delete only genuine data-entry mistakes.

</details>

<details>
<summary><strong>How do I find out who had an item last term?</strong></summary>

Admin → Assets → the item → its history. Every custody event is there: who took it, when, when it
was due, who brought it back and in what condition.

</details>

<details>
<summary><strong>Can two people use it at once?</strong></summary>

The API handles concurrent sessions correctly — it is a proper transactional database and two
simultaneous checkouts of the same item resolve cleanly. But there is one machine, one screen and
one scanner, so in practice it is a queue at a counter.

</details>

<details>
<summary><strong>What happens if the power cuts mid-checkout?</strong></summary>

A checkout is one transaction: the whole cart moves or none of it does. You will not find half a
cart checked out. Sessions live in memory, so everybody is signed out when the server restarts,
which is by design.

</details>

<details>
<summary><strong>The PC died. How bad is it?</strong></summary>

As bad as your last backup. With backups configured you install on a new machine and restore an
archive by date — that is a supported path, not a rescue. Without them, the data is gone. Configure
backups on day one; the system will nag everybody who signs in until you do.

Note the one gap: **photos are mirrored locally only**, so a dead drive loses `uploads/` and its
mirror together. That was a deliberate trade, argued in `docs/design/backup.md` §H.

</details>

<details>
<summary><strong>Can we get our data out if we stop using Stockroom?</strong></summary>

Yes. Every backup is plain CSV per table, plus a readable `inventory.csv` and `accounts.csv`, which
open in any spreadsheet. Press Backup Now and take the folder.

</details>

### Under the hood

<details>
<summary><strong>Why Supabase if none of its features are used?</strong></summary>

It is a convenient Postgres distribution with migrations, seeding and a table editor already set up
and working. Its REST API and Auth service are unused; row-level security is off because the Go
server is the only thing that ever connects. Removing it from the deployment (keeping it for
development) is Phase T5.

</details>

<details>
<summary><strong>Why is there no row-level security?</strong></summary>

Because there are no database clients to defend against. One process connects, as `postgres`, and
every permission check lives above it in `internal/stockroom` where it can be tested. RLS would be a
second place for the same rules to live, and two places is how rules drift.

</details>

<details>
<summary><strong>Why is the checkout limit exactly seven days?</strong></summary>

Because that department chose it. It is `now + 7×24h` at the moment of the request — an exact
instant, not the end of the seventh day — and it is currently a constant. Making it a setting is
Phase T4.

</details>

<details>
<summary><strong>Can I query the database directly?</strong></summary>

Yes — Supabase Studio at <http://127.0.0.1:54323/project/default>, or any Postgres client against
`postgresql://postgres:postgres@127.0.0.1:54322/postgres`. There are no guard rails there; take a
backup first, and remember that the application enforces its rules above the database, so a hand-
written `UPDATE` can create states the app never would.

</details>

<details>
<summary><strong>How do I report a bug or ask for something?</strong></summary>

Open an issue at <https://github.com/Kathir-D/Stockroom/issues>. If you are a school trying to
install it, say so — that feedback is worth more right now than any feature request, because the
install path is the part that has never met a stranger.

</details>

---

## Project status

Stockroom is coursework built for a real department, on a nine-week schedule, and it is feature-
complete but not yet packaged for anybody else.

**Done.** The schema and seed. The Go API: authentication, sessions, permissions, the roster import,
browse and filtering, the full scan/cart/checkout/check-in loop, custody history, overdue handling,
kits and the admin endpoints. The entire frontend, as one shared package rendered by both hosts. The
backup system: configuration in the database rather than `.env`, a scheduler inside the server, CSV
export with a manifest, Google Drive and GitHub as targets, the local photo mirror, and a restore
that validates every checksum, row count, foreign key and sequence before it commits — backtested
end to end against a live database.

**Not done.** No barcode scanner has been bought, so the scan-vs-typed threshold is an untuned
default and no part of the scanning flow has met real hardware. Neither off-site backup target has
been exercised against a real Google account or GitHub repository. The Windows start script has
never run on Windows. And installation still requires a developer toolchain.

**The template track** — everything that stands between this and another school using it — is
[`TEMPLATE-TODO.md`](TEMPLATE-TODO.md). [`TODO.md`](TODO.md) tracks the software itself phase by
phase, and [`CLAUDE.md`](CLAUDE.md) §13 records every decision that shaped it, including the ones
that were reversed.

---

## Contributing

Issues and pull requests go to <https://github.com/Kathir-D/Stockroom>.

Before opening a PR:

1. `./scripts/dev.sh deps` — this also installs the pre-commit hook
2. Make your change, with tests where behaviour changed
3. `./scripts/dev.sh test` — the hook runs this anyway, but running it first is faster than
   discovering it at commit time
4. If the change involved a real trade-off, add a line to `CLAUDE.md` §13 explaining the choice and
   the alternative you rejected. That log is how the next person avoids relitigating it.

Two house rules that reviews will hold you to: **permission checks go in `internal/stockroom`**, not
in the router and never in the UI; and **`tokens.css` is the only file that defines a colour**.

---

## License

[GNU Affero General Public License v3.0](LICENSE).
