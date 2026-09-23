# Stockroom as a template: the work list

`TODO.md` tracks the build of Stockroom *for one school*. This file tracks turning that into
something **another school's teacher can install and run without reading any code**.

Read `CLAUDE.md` for architecture and the decision log. Nothing here changes a product decision in
§2 — the scope stays what it is. This is packaging, configuration, documentation and distribution.

**Revised 2026-09-22.** The previous version of this file had 132 checkboxes, 53 of them in one
phase, and did not distinguish *the product does not work without this* from *this is what good
looks like*. Everything read as equally required, which is how a list becomes a thing nobody
starts. This revision keeps the same thinking and re-sorts it: three phases of load-bearing work up
front, a **Later** section for the good ideas that are not blocking, and a **Cut** section that
records what was dropped and why, because an undocumented cut comes back as an argument.

The reasoning behind each decision was settled in an interview on 2026-09-22 and is recorded in
§Decisions below rather than being re-derived here.

---

## The one sentence that defines "done"

> A media teacher at a school that has never heard of us downloads one file, follows one page of
> instructions, and is checking a camera out to a student within thirty minutes — without
> installing Go, Node or the Supabase CLI, without editing a file in a text editor, and without
> ever seeing a terminal error.

Docker Desktop is the one exception and it is deliberate: see §Decisions.

---

## Where we actually are

The code is more portable than the project is. Almost nothing in `internal/stockroom` or
`packages/ui` knows which school this is. Four things stand between here and a template:

| # | Blocker | Where it lives | Phase |
|---|---|---|---|
| 1 | ~~Installing it means being a developer~~ — **mostly closed 2026-09-22** | `deploy/`, `scripts/install.sh` | **A** |
| 2 | ~~There is no way to produce a barcode~~ — **closed 2026-09-22** | `barcode.go`, `labels.go` | **B** |
| 3 | ~~The seed *is* one school's inventory~~ — **closed 2026-09-22**: demo-only, never loaded on an install | `supabase/seed.sql` | **B** |
| 4 | ~~A school with IDs like `AB12345` cannot sign in~~ — **closed 2026-09-22** | `student_number.go`, `lib/student-number.ts` | **B** |

(2) is the one that was previously misfiled as polish — see the note in Phase B.

**What changed on 2026-09-22.** Phase A is built for macOS and Linux: one `postgres:17` container
instead of the Supabase stack, the migrations and the web UI compiled into a single binary that
applies its own schema at boot, an installer that is also the upgrade path, and a launchd agent /
systemd unit that restarts the server on crash. Proven on a scratch database with no Supabase
anywhere: ten migrations applied, an admin signed in through the browser, a restart re-applied
nothing. What is *not* done is Windows, and fetching a release binary rather than building from a
checkout — so it still takes Go and Node on the machine you install **from**. See `docs/INSTALL.md`.

**Phase B, also 2026-09-22.** A fresh install now opens on a setup guide instead of an empty
catalogue: first admin, the safety-net account, lending rules, equipment (examples, a category
import, an asset CSV, a numbered batch, or one by hand), people, and backups. Label sheets and ID
cards print as PDFs, and the student-number format is a setting. Every wizard step and dialog was
screenshotted against a scratch `postgres:17` and fixed where it looked wrong. The decisions and the
bugs the new tests found are in `CLAUDE.md` §13 (2026-09-22, Phase B).

---

## Phase A — Make it installable

This is the project. Everything else is a day's work once this exists. **Mostly done 2026-09-22**;
what is left is Windows and the release-binary download path, both marked below.

- [x] **Wait for Postgres at start-up instead of exiting.** *(done 2026-09-22, `ef2fb92`)*
      `Open` pinged once and `main` called `log.Fatalf`, which is right for a developer who typed
      the wrong `DATABASE_URL` and wrong for the machine this runs on. A closet PC's failure mode
      is a reboot: the service starts in seconds, Docker Desktop takes the better part of a minute
      to have a container ready, so the database is *reliably* absent at the moment the server
      first asks for it. And because the nightly backup is a goroutine inside this process (§11),
      "the server did not come back" and "the machine stopped backing up" are the same event, with
      nothing on any screen connecting them. `openWithRetry` gives it a two-minute budget with
      1s→5s backoff. It lives in `server/` rather than in `Open` because the other three callers —
      two test helpers and `cmd/restore` — want the opposite behaviour.
- [x] **A plain Postgres container for installs; keep the Supabase CLI for development.**
      *(done 2026-09-22, `deploy/docker-compose.yml`)* One `postgres:17`, bound to
      `127.0.0.1:54322` explicitly rather than with the short `54322:5432` form — Docker's default
      publishes on every interface and opens the host firewall while doing it, which would put the
      database on the school network. `POSTGRES_PASSWORD` has **no default**, so a compose file that
      is run without one refuses to start rather than booting a closet machine with a guessable
      superuser password and never mentioning it. Verified end to end: migrations applied, the
      server ran, an admin signed in through the browser, with no Supabase CLI anywhere.
      - Kong, GoTrue, PostgREST, Realtime, Storage and Studio were all running and all unused (§4
        says so outright), so shipping the stack meant the teacher installs the CLI *as well as*
        Docker and the closet PC runs ~12 containers for the benefit of one.
      - Development is **completely unchanged**: `supabase start`, `db reset`, `seed.sql`, Studio
        and the pgTAP suite all stay exactly as they are — verified by a full `db reset` and a green
        suite after the change. The CLI just does not travel to a school.
      - **The host port stays 54322 everywhere.** Nobody ever types it, so "5432 is conventional"
        buys nothing real, while a second value means every doc, script, comment and CI reference is
        a place to miss one. It also avoids colliding with whatever Postgres a school may already
        run.
      - The password is generated by `install.sh` into the install directory's `.env` at mode 600,
        and is never shown to anybody.
- [x] **Embed the migrations in the Go binary.** *(done 2026-09-22, `supabase/embed.go`,
      `internal/stockroom/migrate.go`)* The server applies whatever is pending at boot, and it is
      **fatal** where the failsafe admin and the backup settings are not: a schema that is not the
      one the binary was built against fails on its first real query, at a counter, with a student
      holding a camera.
      - The embed lives in `supabase/`, so the runner reads the **same files the CLI reads**. A copy
        under `internal/` was the alternative and is two schemas that agree until somebody edits
        one.
      - The bookkeeping table is **Supabase's own** `supabase_migrations.schema_migrations`, not a
        second one. Two tables means a database migrated by the CLI looks unmigrated to the server,
        so the first production start after a `db reset` would replay `create table profiles` over a
        live schema. It is also the table `schemaVersion` already reads for the backup manifest, so
        the restore's version check keeps working on an install the CLI has never touched.
      - `20260826180000_grant_service_role.sql` had to be made portable: `service_role`, `anon` and
        `authenticated` do not exist on a plain Postgres, so unguarded it stopped a fresh install at
        migration two of ten with an error naming a Supabase concept the reader had deliberately
        never installed. Now guarded on the roles existing, via `execute` — a literal `GRANT` inside
        an `if exists` still fails at parse time.
      - One transaction per file, and an advisory lock over the run. A file whose name has no
        version prefix is an **error, not a skip**: a migration silently not running is the failure
        the whole mechanism exists to prevent.
      - This is also the **upgrade story**: a new binary carries the files and the database moves
        with it. Without it, every install *and every release forever* is a support ticket that
        starts "run these SQL files in this order".
- [x] **Embed the web UI in the Go binary.** *(done 2026-09-22, `web-app/embed.go`,
      `server/ui.go`)* The deliverable is one binary: API, UI and migrations, 19 MB.
      - Vite's `assetsDir` had to move from `assets` to `static`. The API already owns
        `GET /assets` and `GET /assets/{id}`, so a default build puts `/assets/index-a1b2c3.js`
        inside the equipment catalogue's route and the router answers a JavaScript request with
        "asset not found". Renaming the bundle directory is the fix; renaming a documented endpoint
        to suit a bundler is not.
      - `dist/.gitkeep` is tracked, mirroring `desktop-app/frontend/dist`, because `go:embed` fails
        the build when its directory is absent and `go build ./...` has to work on a fresh clone.
        `webapp.Present()` is what tells "nobody ran npm run build" apart from "the UI is missing" —
        same bytes, very different problems — and the server says so in words instead of serving a
        blank page.
      - The UI reads its base URL off `window.location.origin` in a production build, so
        `SERVER_ADDR` stops being load-bearing and CORS never comes into it: a same-origin request
        is not subject to CORS at all. The allow-list stays for the dev loop and the Wails window,
        which are genuinely separate origins; the desktop host is still told its base URL
        explicitly. That deletes the single most confusing failure mode in the system for an
        install (§13, 2026-09-15: a blocked preflight and a dead server are indistinguishable in
        the browser).
      - The Wails app stays the nicer local window, not a requirement.
- [x] **A release workflow.** *(written 2026-09-22, `.github/workflows/release.yml`)* The four
      targets, `checksums.txt` and generated notes, on a `v*` tag. `workflow_dispatch` builds the
      matrix **without publishing**, so the workflow can be proven before a tag depends on it, and
      the UI build runs before the Go build with a `test -f web-app/dist/index.html` gate after it —
      the other order silently ships the tracked empty directory.
      - **Not yet exercised.** No tag has been pushed and no artefact has been downloaded onto a
        machine that is not this one, so `install.sh` still builds from a checkout rather than
        fetching a release. Writing the download path against binaries that do not exist would be
        an untested path in the one place a school meets this project first.
- [~] **`install.sh` (done), `install.ps1` (not started).** *(2026-09-22, `scripts/install.sh`)*
      macOS and Linux. It checks Docker is **running** rather than installed, builds the UI and the
      binary, writes a `.env` with a generated database password at mode 600, prompts for the
      failsafe admin, starts Postgres, installs the service and waits for `/health` before claiming
      success. A second run is the upgrade path and **takes a `pg_dump` first, from inside the
      container** so no Postgres client is needed on the host; a dump that fails stops the upgrade
      rather than continuing, because it is the only protection against a migration that goes wrong.
      `.env` is never overwritten — backup settings live in the database, and an installer that
      rewrote the seeds every release would be the "a value an admin typed reverts on the next
      restart" failure Phase 7 rules out.
      - **Still to do.** It requires Go and Node to be installed, because it builds from a
        checkout rather than downloading a release binary — the deliberate gap noted under the
        release workflow above. It does not *install* Docker (`winget install
        Docker.DockerDesktop`, a Homebrew cask, otherwise the download link and what to click); it
        only checks for it and waits. And it opens `/`, not the first-run wizard, because the
        wizard is Phase B.
      - **`install.ps1` is not started**, and with it the WSL2-restart sentence ("restart, then run
        the same command again" — not a `RunOnce` resume, see §Cut).
- [x] **Register it as a service** *(macOS and Linux done 2026-09-22,
      `deploy/com.stockroom.server.plist.template`, `deploy/stockroom.service.template`; Windows
      not started)* — with **restart on crash and backoff**, and one shared entry point,
      `deploy/stockroom-run.sh`, so there is no macOS copy and a Linux copy to drift apart. It waits
      for the **Docker daemon** (not the binary) for three minutes, brings the container up, and
      `exec`s the server so the service manager supervises the real process rather than a live
      wrapper around a dead one.
      - **The Mac is a LaunchAgent, not a LaunchDaemon, and that is a real cost.** Docker Desktop
        only runs inside a user session, so a boot-time daemon would start, find no Docker and give
        up. The agent starts at login instead, which means the closet Mac must be set to log in
        automatically or a power cut leaves it at the login window — running nothing, backing up
        nothing, and saying so nowhere. `docs/INSTALL.md` says this in those words. Linux has no
        such problem and gets a real system unit.
      - Why it was never optional: the nightly backup lives inside the server, so "the server is
        running" and "backups happen" are the same fact, and until now that depended on somebody
        having left a terminal window open.
      - **Still to do:** offer "restart now to check everything comes back on its own" at the end
        of the install, and actually reboot this machine once to confirm it. A school finding out
        on day forty that it never auto-started is a school that lost forty days of backups, and
        that is not something a passing install script has proven.

---

## Phase B — Make it usable by a school that isn't yours

- [x] **Barcode generation.** *Previously filed under "Hardware, stickers and the physical setup",
      near the bottom, described as an adoption cliff. It is not an adoption cliff, it is a missing
      feature.* The entire product is barcode-driven — §1.5 makes scanning the single "track it"
      action — and there is currently no way to produce a barcode for either an asset or an ID
      card. A school that installs Stockroom today has a system they cannot use.
      - `github.com/boombuler/barcode` (MIT, pure Go, Code 128) plus a Go PDF writer. Not a
        browser-rendered print page: browsers apply their own margins and scaling, and 2mm of drift
        ruins a whole sheet of adhesive labels. A server-generated PDF at exact physical dimensions
        prints the same everywhere, works offline and stays inside the one binary.
      - **`GET /assets/labels.pdf?ids=...`** with a **size picker**: 30-per-sheet (Avery
        5160 / L7160), 80-per-sheet for batteries and small gear, and a single-label format. The
        serial as Code 128 with the asset name under it.
        *Built as `POST /assets/labels.pdf` (a long id list does not fit in a URL) with Small,
        Medium, Large and one-per-page, each in US Letter and A4, named by what they go on rather
        than by part number. The print dialog picks the paper from the browser's locale.*
      - **Serials have to be short for the small label.** It fits about **9 characters on Letter
        and 7 on A4** before the bars drop below the 0.25 mm that decodes at 203 DPI. `CLAUDE.md`
        §6.2's example serials were `T7IBAT-001` (10), which would not print on the label meant for
        batteries; they are now `T7B-001`. Say this in the admin guide.
      - **`GET /assets/{id}/barcode.png`**, rendered inline on the asset detail dialog and
        downloadable from the same endpoint — one endpoint, two features. The inline render is also
        the recovery path for the failure a school will actually hit: the sticker falls off, and
        they find the asset by name, read its serial and reprint.
      - **A printable ID-card layout** from the same generator, for schools whose cards carry no
        barcode or encode something other than the student number. Without it, scan login — the
        headline feature — does not work for them at all.
      - [ ] *Not done — belongs in `docs/ADMIN-GUIDE.md`.* Sticker guidance alongside it: matte not glossy, where they survive on a lens barrel versus
        a body, laminate anything that goes outdoors.
      - [x] *In the wizard's equipment step, as a tip.* A manufacturer's existing barcode is a valid serial. Plenty of gear already wears a unique
        barcode from the factory, which means **no sticker to print at all** for those units. Say
        this in the wizard; it is a real hour saved and nobody guesses it.
- [x] **A fresh production database starts empty.** `seed.sql` becomes demo-only and says so at the
      top: it keeps the 63-node tree, the 12 assets, the kit and the two dev accounts, it loads on
      `supabase db reset` during development, and it **never** loads on an install. The only way
      into a fresh install is the wizard or the `.env` failsafe admin (§7), which is exactly what
      the failsafe is for and is already proven by `cmd/restore`'s zero-account path.
- [x] **A category-tree import**, `POST /categories/import`, admin-only. Building an eight-Type
      tree through *New category* is roughly sixty dialogs, it is the **first** thing a new school
      does, and it is where they stop.
      - **Accepts both** the indented-text shape the example files use and a CSV of
        `type,category,model`, sniffed on upload. The text form keeps the example files readable as
        documents; the CSV form is what a department is more likely to already have.
      - Idempotent — re-running adds what is missing and changes nothing else — because the first
        attempt will have a typo in it.
      - Sets `sort_order` from document order, the way the seed does. That column exists precisely
        because document order is not alphabetical order (§13, 2026-09-13).
      - Watch the constraint that bites: **`categories.name` is unique across the entire table**,
        not per parent. Two departments both having an "Accessories" node is an import failure with
        a confusing message unless the importer says so in plain words.
- [x] **An asset CSV import**, `POST /assets/import`, columns `serial_number,name,category,model,
      status`, upserting by serial. A school with 300 items cannot type them into a dialog.
      `roster.go` is already the pattern: per-row errors collected with line numbers, the rest
      still landing. No column-mapping screen, no dry run, no undo token — see §Cut.
      - **Duplicate detection with a readable message.** A serial that already exists is
        updated, and the report says what it replaced — *"You already had an item with this
        serial: Canon 70-200mm (added 3 Mar 2026). It was updated to match this row."* — rather
        than a database constraint error or a silent overwrite. A serial repeated *within* the
        file is refused, naming the line it first appeared on.
- [x] **Bulk add N units of one model.** *"Add 12 × Canon LP-E6 battery"* generates `LPE6-001`
      through `LPE6-012`, **shows the list of serials before committing**, and offers the label
      sheet immediately. Linear stock — batteries, SD cards, bags, cables — is most of the unit
      count in a real department and all of the tedium. Let the prefix be edited and remember it per
      model, so next year's re-order continues the numbering instead of colliding with it.
      *Built without the per-model memory: the numbering continues from the highest serial
      already using that prefix, which gives the same re-order behaviour with nothing stored.*
- [x] **A student-number format setting.** Digits-only, 1–32 today (`NormalizeStudentNumber`,
      `password.go`). Plenty of schools issue IDs like `AB12345`, and for them Stockroom currently
      **does not work at all** — the sign-in field filters their ID to nothing and the message never
      fires. Every other policy constant is a preference; this one is a hard blocker.
      - The wizard offers **Digits only / Letters and numbers / Custom**, defaulting to digits-only
        so nothing changes for the existing install. Custom takes a regex with a live test field
        ("paste a real ID and we'll show you whether it matches"), because a bad pattern that ships
        silently locks everybody out of sign-in and the failure is invisible until someone scans a
        card.
      - **`sign-in.svelte`'s digit filter has to change with it, and so does the scan-vs-typed
        comparison that runs the keystroke buffer through that same filter** (§13, 2026-09-15).
        This is the one item in this file that touches genuinely subtle code. Read that decision log
        entry before starting and keep the six `scanner.test.ts` regressions green.
- [x] **A seven-step first-run wizard** at `/setup`, reachable **only while `profiles` is empty** —
      an unauthenticated route that stops existing the moment there is an account, which is the only
      safe shape for it.
      *Diverged, deliberately: only **creating the first admin** is gated on an empty `profiles`.
      `install.sh` asks for the failsafe and the server creates it at boot, so on a real install
      that table is never empty and the wizard as written would never appear. The rest is
      admin-only, resumed from `app_settings.setup_step` until `setup_completed_at` is set. Lending
      rules are stated rather than configurable — the settings are under §Later. `CLAUDE.md` §13.*
      - One binary, one front door. The wizard always exists; `INSTALL.md` documents how somebody
        CS-literate skips it by pre-filling settings and starting the server, which is exactly what
        happens today.
      - Keep `ADMIN_STUDENT_NUMBER` / `ADMIN_PASSWORD` as the failsafe they already are. The wizard
        is the pleasant path; the failsafe is the floor. Do not collapse the two.

      | # | Step | Skippable |
      |---|---|---|
      | 1 | Welcome — what is about to happen, and how long | — |
      | 2 | **Create your admin account** | **no** |
      | 3 | **The safety net** — the failsafe admin password | **no** |
      | 4 | Lending rules — how long, and what happens when something is late | yes |
      | 5 | Build your catalogue, and add your equipment | yes |
      | 6 | Add your people | yes |
      | 7 | **Turn on backups**, then the cheat sheet | yes, with a warning |

      Design rules: each step saves as it completes, so closing the laptop resumes where it stopped;
      every step is skippable except 2 and 3, because a person who cannot skip is a person who will
      enter rubbish to get past a screen; plain words only, never "instance", "schema", "migration",
      "seed"; a progress rail so the end is visible from the start; a *Why?* link on every
      non-obvious field. **Write the copy before building the screens** — the drafted text in
      §Copy below is load-bearing, not decoration.
      - **Backups (step 7) pre-fill a local folder path but require the admin to confirm it.** The
        2026-09-21 relative-path decision is the reason: a folder that is accepted without being
        looked at produces a complete archive, a manifest that verifies, every screen reporting
        health, and files nowhere the admin will ever look. Finish with *[ Run a backup now and show
        me it worked ]*, which is the only proof that matters.
- [x] **Example data, and a prompt to remove it.** Ship an `examples/` folder — a roster CSV, an
      asset CSV, two category trees — all obviously fake (`Example High School`, numbers starting
      `900`). These double as the fixtures for the install walkthrough, so the docs and the files
      cannot drift apart. Offer to load them at setup, so somebody can try the system with three
      cameras in it before spending an afternoon entering their inventory.
      - Seeded example rows get a **reserved UUID prefix** (`00000000-…-9xxx`) rather than an
        `is_example` column, so there is no migration and no flag that every real install carries
        forever. It is invisible in the UI and reads as magic to the next maintainer, which is why
        it is written down here.
        *Not built that way: the importers generate their own ids, so a reserved id would have
        needed an example-only insert path. Examples are recognised by content instead — an
        `EXAMPLE-` serial **and** an `Example …` name, or a `90000x` number **and** the first name
        `Example`. Admin → Assets shows a remove-the-examples banner while any are loaded.*
      - **When real data is imported, prompt to delete the examples.** Fake cameras that outlive
        setup are worse than no demo at all, because the catalogue looks authoritative and is wrong.
- [x] **Move `Catagories.md` to `examples/categories.media-department.md`** and stop calling it a
      source of truth. It becomes *an* example tree alongside at least one more — a theatre or a
      science department's — so the shape reads as a pattern rather than a spec.

### §Copy — the wizard's words

Restored from the original plan (T6.8), renumbered to the seven steps that were built. Steps 1, 3,
5, 7 and Done shipped close to this; where the build differs, the note says so. **Do not ship
placeholders** — every one of these is the difference between a confident user and a hesitant one.

**Step 1 — Welcome**

> **Welcome to Stockroom.**
> This sets up equipment checkout for your department. It takes about twenty minutes, and you can
> stop at any point and pick up where you left off.
> Here is what we'll do: create your account, tell Stockroom what equipment you have, add the
> people who can borrow it, print barcode stickers, and turn on automatic backups.
> You will not need to install anything else, and Stockroom never sends your data anywhere you
> haven't set up yourself.
> *[ Let's go ]*

**Step 2 — Create your admin account** *(the sign-in screen, while there are no accounts; the ID
format comes first, because the admin's own number has to fit it)*

> **Create your account.**
> You'll use this to add equipment, manage people, and see who has what.
> **Your ID number** — the number on your school ID card. If your card has a barcode, scan it now
> and we'll fill this in. *[ field ]*
> **Your name** *[ first ]* *[ last ]*
> **A password** — you'll only need this when you type your number instead of scanning it. At least
> 8 characters. *[ field with a reveal button ]*
> *Why a password if I can scan?* → Scanning is the fast way in at the counter. Typing your number
> needs a password, so somebody who simply knows your number can't sign in as you.

**Step 3 — The safety net** *(cannot be skipped)*

> **One thing to write down.**
> This is a spare administrator account that Stockroom re-creates every time it starts. If a screen
> ever breaks, or the last admin account is deleted by accident, this is how you get back in.
> **Spare ID number** *[ 900000 ]* **Spare password** *[ generated, shown in full ]*
> **Save this somewhere that isn't this computer** — a password manager, or a sealed envelope in a
> drawer. If the computer fails and you have to set Stockroom up again, this is what gets you back
> into your restored records.
> ☐ I have saved these somewhere safe *(required to continue)*

*Built: the password is shown, not masked — the whole point of the step is writing it down. If
the installer already made a failsafe, the step says so instead.*

**Step 4 — Lending rules**

> **How does your department lend equipment?**
> **How long can somebody keep something?** *[ 7 ] days* — students pick a return date when they
> check out, and they cannot pick one further away than this.
> **When something is late:** ( ) Stop that person borrowing anything else until it comes back
> *(recommended)* ( ) Just show a warning
> You can change both of these later in Settings.

*Built as "How lending works." — the rules stated, not chosen, because `max_checkout_days` and
`overdue_blocks_checkout` are not settings yet (§Later). When they are, this is the copy.*

**Step 5 — Add your equipment**

> **What's in your store cupboard?**
> Every camera, lens, light and battery gets its own entry, so you always know which one came back.
> Pick whichever of these suits you — you can use more than one, and you can add more any time.
> **[ Scan them in ]** *Fastest if you have the scanner plugged in. Stick a label on an item, scan
> it, type its name, move to the next one. About eight seconds each.*
> **[ Upload a spreadsheet ]** *If you already have a list. We'll show you what we found before
> anything is saved.*
> **[ Add a batch ]** *For twelve identical batteries, twenty SD cards — we'll number them for you.*
> **[ Add one by hand ]**
> **[ I'll do this later ]**

*Built without "Scan them in" (scan-to-create is cut, §Cut) and with "Try it with examples" and
"Upload your categories" added. "We'll show you what we found before anything is saved" is not
true of the asset import — there is no dry run (§Cut) — so the built copy says only "If you already
have a list. Save it as CSV first." and the report comes afterwards. The manufacturer's-barcode tip
sits under the choices.*

**Step 6 — Add your people** *(built as "Who can borrow?": the ID format card and a class-list
upload; no drafted copy existed)*

**Not built — Test your scanner** *(needs a scanner to write against; §Later)*

> **Let's check your scanner works.**
> Scan any barcode — an item, an ID card, a book, anything. *[ live field ]*
> ✅ *We saw `T7B-001` and it arrived in 34 ms, which is well within the range Stockroom reads as
> a scan. Your scanner is set up correctly.*
> ⚠️ *We saw the characters but no Enter at the end. Your scanner needs its "add Enter after scan"
> setting turned on — it's usually a barcode printed in the scanner's manual. [What this means]*
> ❌ *Nothing arrived. Check the scanner is plugged in and that its light comes on when you press
> the trigger. [More help]*

**Step 7 — Backups**

> **Protect your records.**
> Stockroom backs itself up every night, automatically. Choose where those backups go.
> **On this computer** *[ folder picker, with a sensible default already filled in ]* — takes a
> minute to set up and covers most problems.
> **Also send them somewhere else** *(recommended, optional)* — Google Drive or GitHub, so a failed
> hard drive doesn't take your records with it. You can set this up now or later in Settings.
> *[ Run a backup now and show me it worked ]* ← do this. It is the only proof that matters.
> ⚠️ If you skip this entirely, Stockroom will remind everybody who signs in until it's set up.

*Built: the folder is an example placeholder the admin must type over, not a pre-filled value —
see the relative-path decision above. "Skip for now" stays a quiet button until a backup has run.*

**Done**

> **You're ready.**
> *(a summary: 247 items · 31 people · backups on)*
> **[ Print the student instructions ]** — one page for the wall above the computer.
> **[ Print your barcode stickers ]** — for the items that need one.
> **[ Open Stockroom ]**
> Next: scan your own ID card at the sign-in screen and check something out, so you've seen what
> your students will see.

*"Print the student instructions" waits on `docs/STUDENT-GUIDE.md` (Phase C).*

---

## Phase C — Publish and prove

- [ ] **Check the working tree, not just history.** Git history is already clean: no `.env`, no
      `uploads/`, no roster CSV and no backup zip has ever been committed (verified with
      `git log --diff-filter=A`). But `uploads/` *exists* on this machine and may hold real student
      photographs; `.run-logs/`, `.cache/`, any `backup/` folder and any `.env` are the same. One
      `git status --ignored` and a careful read before the first `git add -A` on a public repo.
- [ ] **Scan the history for secrets anyway.** `gitleaks detect --no-git=false` or
      `trufflehog git file://.`. Free, two minutes, and the one thing that cannot be undone after
      publishing.
- [ ] **Search the docs for real names and numbers.** `CLAUDE.md`, `TODO.md` and the design docs
      quote real data in places ("Admin Admin or Test User", student numbers, the department's
      inventory). Decide per instance whether it is sample data or somebody's actual name.
- [ ] **Extend CI to a Windows runner.** `tests.yml` proves it on one runner. **Windows *is* the
      deployment target and `dev.ps1` has never been run on real Windows hardware** (§9, still
      open). This is a correctness gap, not a polish item — it is the single most likely thing to
      break for the first school that tries.
- [ ] **Branch protection on `main`**: require a PR and the `tests` check, no direct pushes. You
      already work on `testing` and PR into `main`; this makes the habit structural. Keep the
      docs-only CI skip (`CI.md`).
- [ ] **Fill in the repository description and topics.** `school`, `inventory`, `checkout`,
      `barcode`, `education`, `equipment`, `go`, `svelte`, `self-hosted`. Topics are the only
      discovery mechanism GitHub gives you and they cost thirty seconds.
- [~] **`docs/INSTALL.md`** — *written 2026-09-22 for macOS and Linux, against the Phase A
      install path that now exists.* Still to do: Windows, a screenshot per step, and the
      twenty-minute timing claim, none of which can be honest yet. Document the PowerShell
      execution-policy prompt with the exact words on the button, and the manual path for somebody
      who would rather pre-fill settings than use the wizard.
- [ ] **`docs/STUDENT-GUIDE.md`** — one page, printable, sticky-taped to the wall above the PC.
      Scan your card. Find your gear. Add to cart. Pick a date. Scan it back when you return it.
      Cheap, and it closes the most common adoption failure: a system that works and a group of
      students who were never told how to use it.
- [ ] **`docs/ADMIN-GUIDE.md`** — the teacher's manual. Adding an asset. Importing a roster.
      Building the category tree. Printing stickers. What to do when a student leaves with a
      camera. Reading the overdue list. Handing the system to next year's teacher.
      `docs/BACKUP-SETUP.md` is already written at exactly this register — match it.
- [ ] **A data-export answer.** An admin-panel **Export everything** button producing the same zip
      the backup writes, named as an export rather than a backup so a teacher looking for "export"
      finds it — plus one page in the admin guide explaining what each CSV column means. "We want to
      stop using Stockroom" should be a documented path, not a rescue operation; it is also what
      makes adopting it low-risk.
- [ ] **Mark the GitHub backup target experimental**, in the UI and in the docs. It has never
      talked to a real account — no repository created, the REST path unit-tested and hand-read but
      never round-tripped (§13, still open). Drive is verified as of 2026-09-21. Either test it
      properly or say plainly that it is untested; shipping it silently is the one thing that is
      not allowed, because a school that picks it is choosing where their records go.
- [ ] **One clean install on a machine that is not yours.** A clean VM or a borrowed laptop: no Go,
      no Node, no Docker pre-installed, no repository clone. Follow `INSTALL.md` word for word and
      **change nothing while doing it** — every time you reach for the source, that is a bug in the
      docs, and it goes in a list. **Time it.** If it takes more than thirty minutes, the number
      goes in the README until it does not.
- [ ] **Do a real checkout on it**: import a roster, import a category tree, import assets, print a
      sticker sheet, scan a card, scan an item, check it out, check it back in. Then **reboot the
      machine** and confirm the server comes back by itself and the backup still fires.
- [ ] **Hand it to somebody who has not seen it** and watch without helping. Record three numbers:
      minutes from download to first successful checkout, the number of times they asked for help
      (**target: zero**), and the number of times they saw something they could not act on
      (**target: zero**). Every one of those moments is a bug filed against Phase A or B.
- [ ] **Then a pilot**: the school's own media department, one term. Everything above is theory
      until then. It is not a test of the *template* — you will be there to fix things — so a second
      department in the same building is the better second test.
- [ ] **Tag `v1.0.0` only after the clean-machine install passes.** A 1.0 that does not install is
      worse than a 0.9.
- [ ] **Carried over from [`TODO.md`](TODO.md), which is otherwise closed:**
      - [ ] Buy a scanner and tune `SCAN_KEY_THRESHOLD_MS` against it. Everything about scanning is
            currently a reasoned 50 ms guess that has never met hardware. Blocked on hardware.
      - [ ] Run both backup targets at once, break one, and confirm the other still succeeds and
            the status names which failed.
      - [ ] A configuration-only run-through from a fresh install, without opening a text editor.

---

## Decisions

Settled 2026-09-22. Recorded with the alternative that lost, in the style of `CLAUDE.md` §13.

- [x] **Licence: AGPL-3.0, unchanged.** The goal is that anyone who profits from the code has to
      open-source what they built on it. AGPL is the strongest available answer to that, and the
      only one whose obligation survives somebody hosting a modified copy as a paid service. Worth
      knowing and accepting: **no OSI licence forces publication of purely private modifications** —
      copyleft triggers on distribution — so a school that modifies Stockroom for its own closet PC
      owes nothing under any licence. Accepted cost: some district IT departments reject AGPL on
      sight, without reading it.
- [x] **Distribution: releases only.** Schools never clone. They download an installer script and a
      binary from the Releases page; upgrades are "run it again". A template repository was the
      alternative and loses on upgrades — a template copy has no shared history and **cannot merge
      later changes**. Split source/deploy repos are the right answer when a second school actually
      exists, and not before.
- [x] **Say nothing about support, maintenance or succession, anywhere.** No promise, no
      disclaimer, no named future maintainer. The AGPL already carries a no-warranty clause. The
      consequence, accepted: a school's IT and data officer get no written answers, which is usually
      where a district says no — so this is a decision against near-term adoption, not an oversight.
- [x] **Docker stays, and the installer installs it where it can.** The honest alternatives were
      bundling Postgres binaries per platform (better for the teacher, meaningfully more packaging
      work, and you own Postgres upgrades) or rewriting on SQLite (every query, and the pgTAP suite
      gone). Docker is the right answer; what matters is stating the reason confidently in the
      docs, because "why do I need Docker" is the first question and a confident answer is worth
      more than a clever one.
- [x] **Supabase is a development tool, not a deployment.** See Phase A. The decisive fact was not
      the container count: shipping the stack puts **Studio on the closet PC, on port 54323, with no
      authentication** — full read/write on every table including the roster, for anyone who walks
      past and opens a browser.
- [x] **The product is called Stockroom for everybody.** No `site_name`, no logo upload, no
      `GET /branding`. A school's name on the sign-in screen is the definition of a nice-to-have,
      and the endpoint existed only because sign-in has no session to read settings through.
- [x] **Scripts now, an `.exe` later.** `install.ps1` and `install.sh`, with a one-line command on
      the download page. This removes the SmartScreen question entirely (a code-signing certificate
      costs money annually and needs a legal identity to issue to, which is awkward for student
      coursework) and replaces it with a PowerShell execution-policy prompt the docs cover. Revisit
      a real installer only if a school actually stalls on that step.
- [x] **"Student" everywhere.** A `member_noun` setting (student / member / staff / borrower) would
      roughly double the number of places this fits — a makerspace, a staff lending library — for a
      couple of hours of work. Declined: the audience is schools, and `CONTEXT.md` fixes this
      vocabulary deliberately.
- [x] **Multi-department in one school is out of scope, and written down.** Two departments, one PC,
      one database, separate inventories is what a school asks for second. The answer is one install
      per department, on a second port. Building it means a department column on assets, categories
      and profiles threaded through every query and permission check — including `custody.go`, whose
      correctness the whole product rests on.
- [x] **The GitHub backup target ships marked experimental** rather than being removed or tested
      first. Removing it would delete `target_github.go` and the orphan-branch prune — the one
      irreversible write the app makes — and it is the target that needs no installed software and
      is hardest for a school firewall to block. So it stays, labelled honestly.

---

## Later

Good, not blocking. Each is an afternoon whenever it is wanted.

- [ ] **The remaining policy settings**, each an `app_settings` column with today's value as the
      default so nothing changes for the existing install:
      - `max_checkout_days`, default 7 (`custody.go`, `packages/ui/src/lib/due.ts`). **Both** layers
        must read it, or the date picker offers a date the server refuses (§13, 2026-09-12: the cap
        is an exact instant, `now + N×24h`).
      - `overdue_blocks_checkout`, default true. Some departments want a warning rather than a
        refusal. The admin override stays either way.
      - The session idle timeout, moving from `SESSION_IDLE_MINUTES` in `.env` into `app_settings`
        so it stops being a file edit, like everything else did in Phase 7.
      - The scan threshold, currently `SCAN_KEY_THRESHOLD_MS = 50` in `scanner.ts` and untuned. A
        different school buys a different scanner; keep Ctrl+Shift+D as the way to measure what to
        set, and document that procedure in the admin guide rather than a code comment.
- [ ] **Leave the three-level category tree alone.** It is capped at depth 3 in
      `categories_admin.go` and that cap is load-bearing for the browse UI. A school with a
      four-level taxonomy can file assets at any node (ADR 0001). Write that down instead of
      building arbitrary depth.
- [ ] **Screenshots and a short screen recording** with the example data loaded, in the README —
      sign-in, browse, a scan checking an item in, the overdue list. This is how adoption actually
      happens.
- [ ] **A scanner buying guide** (`docs/HARDWARE.md`): what "USB HID keyboard-wedge" means in shop
      terms, two or three specific models with prices, what to avoid (vendor software,
      Bluetooth-pairing-only, 2D imagers configured for a prefix), and how to verify one in five
      minutes with Ctrl+Shift+D before the return window closes. **Blocked on buying one.**
- [ ] **A physical setup page**: where the PC goes, the scanner on a short cable at counter height,
      a monitor a student can read standing up, and the fact that the machine must stay awake —
      sleep settings are a real failure mode for a nightly backup goroutine.
- [ ] **`docs/FAQ.md`**, kept as the single place questions accumulate, in the asker's words.
- [ ] **`docs/UPGRADING.md`** — empty until there are two versions, and then urgent.
- [ ] **Photos without friction** on the asset form: a webcam capture button and drag-and-drop. A
      photo per item is what makes browse usable by a student who knows what the thing looks like
      but not what it is called.
- [ ] **A path for "our students don't have ID numbers"** — assign from a range (`900001` upward)
      and print ID cards from the same generator. Makes Stockroom work for a club or a makerspace.
- [ ] **A path for "I don't have a CSV file"** — paste a list of names, or add people one at a time.
- [ ] **`stockroom doctor`, three checks**: is the Docker daemon responding (not the binary — the
      daemon), is port 8080 free **and what is holding it** ("Port 8080 is in use by Skype" is
      actionable; "bind: address already in use" is not), and is there disk space with a projection,
      because the photo mirror only ever grows (§11).
- [ ] **A copyable support bundle**: doctor output, install log, server log tail and the settings
      **with every secret stripped**, zipped, with the path shown.
- [ ] **Uninstall instructions**, and a *move to a different PC* procedure — back up, install
      elsewhere, restore. The restore path already supports this exactly; it has to be written down
      as a supported operation rather than a disaster one. An uninstaller should ask about the data
      and take a final backup before removing anything.
- [ ] **Reconcile the docs that disagree.** After Phases A and B these say things that are no longer
      true: `CLAUDE.md` §3/§9 (the deployment model and the setup steps), §6.2 (`Catagories.md` as
      the seed's source), `TODO.md`'s framing, `CONTEXT.md` ("Not configured" says the admin edits
      `.env`), `CI.md`, and `docs/BACKUP-SETUP.md` §1. **Amend each as you land its phase** — doing
      it as one pass at the end is a mistake, and the header of `CLAUDE.md` already tracks
      amendments this way.
- [ ] **Keep `CLAUDE.md` §13 as it is.** It is the best thing in the repository and it is not
      documentation for schools; it is documentation for whoever maintains this next, including
      future you. Do not dilute it into a user guide.

---

## Cut

Recorded rather than deleted, because an undocumented cut comes back as an argument.

- **Scan-to-create.** Scanning an unknown barcode in the admin catalogue opening a pre-filled
  two-field form. Genuinely the highest-value idea in the original file — roughly eight seconds per
  unit, two afternoons of inventory entry becoming ninety minutes — and cut anyway, deliberately.
  **The consequence is real and should be watched**: CSV import and bulk-add are now the only bulk
  paths in, so a department with 200 assorted items has a CSV with no dry run and no undo, or a
  dialog per item. If the pilot stalls on data entry, this is the first thing to reconsider.
- **The column-mapping screen, the dry run and the 24-hour undo token.** For the same reason and
  with the same caveat: CSV import matters *more* now that scan-to-create is gone, not less.
- **Most of the bootstrapper apparatus**: OS/arch detection, checksum verification, the offline
  `docker save` bundle, the install manifest, the Update·Repair·Uninstall menu, BIOS virtualization
  detection, proxy handling, antivirus exclusions. This is engineering for strangers on hostile
  networks. The installer's honest job is: check Docker, fetch the binary, write `.env`, start, open
  the browser — plus the second-run branch in Phase A, which is the minimum that makes the upgrade
  story true.
- **`RunOnce` reboot-resume.** Kept briefly and then cut, because the second-run branch makes it
  unnecessary: "restart, then run this command again" is a path that is already built and already
  tested, whereas RunOnce is the fiddliest Windows work in the file, fires only on the WSL2 branch,
  and cannot be tested here — `dev.ps1` has never run on real Windows. It costs the teacher one
  double-click.
- **Eight of the doctor's eleven checks**: system clock accuracy, HID device attached, power/sleep
  settings, folder write permissions, administrator rights, virtualization availability, OS version
  and architecture. Charming, and they will never fire.
- **The in-app updater** (backup → stop → swap → migrate → verify → auto-rollback). Substantial and
  risky; re-running the installer covers it.
- **The tray icon.** Needs a second platform-specific process; the doctor shortcut covers most of it.
- **T9's community apparatus**: Discussions, `CODE_OF_CONDUCT.md`, `CODEOWNERS`, issue templates,
  the pinned "Start here" discussion, Dependabot. Fourteen items for a repository with no outside
  contributors. Branch protection, the release workflow and Windows CI earn their place; these do
  not, yet.
- **`docs/PRIVACY.md`** and the FERPA/GDPR note. Cut with the support and succession text, and the
  same consequence applies: a data officer asking what personal data Stockroom holds, where it goes
  and how long it is kept has nowhere to be pointed.
- **The one-page purchase-order PDF**, `CONTRIBUTING.md`, and a `SECURITY.md` — all downstream of
  deciding to say nothing about support. See §Still open.
- **A theme editor.** `tokens.css` is the design system (`design-system.md`) and one school wanting
  maroon is not worth a colour picker plus the contrast bugs that follow.

---

## Still open

- **When the repository goes public.** Deferred until more of Phase A exists. The constraint is
  only that T0's licence decision and Phase C's secret scan both come first, and the licence is now
  settled.
- **Whether `SECURITY.md` gets written at all.** "Say nothing about support" and "a school's IT will
  ask about the threat model" pull in opposite directions. The honest threat model is short and
  already known: localhost-only, no RLS, the database superuser password is generated at install,
  and a backup archive contains a roster of working scan-login credentials.
- **What a school owes its students.** There is no "delete my data" flow beyond `DELETE /users/{id}`,
  which cascades the custody trail away entirely. That is either fine or a feature request.
- **`scripts/dev.sh test` is not idempotent.** Found 2026-09-22: running it twice in a row without a
  `supabase db reset` in between fails `080_seed.test.sql` ("nothing seeded is overdue"), because
  the Go suite leaves the database mutated and pgTAP asserts against seed state. Same class as the
  `-p 1` decision (§13, 2026-09-17). It makes the pre-commit hook fail for a reason that has nothing
  to do with the commit.
