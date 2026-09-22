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
| 1 | **Installing it means being a developer** | `scripts/dev.sh`, the whole toolchain | **A** |
| 2 | There is **no way to produce a barcode**, and the product is barcode-driven | nothing exists | **B** |
| 3 | The seed *is* one school's inventory, and ships a known admin login | `supabase/seed.sql` | **B** |
| 4 | A school with IDs like `AB12345` cannot sign in at all | `password.go`, `sign-in.svelte` | **B** |

(1) is the project. (2) is the one that was previously misfiled as polish — see the note in
Phase B.

---

## Phase A — Make it installable

This is the project. Everything else is a day's work once this exists.

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
- [ ] **A plain Postgres container for installs; keep the Supabase CLI for development.**
      One service, `postgres:17`, in a `docker-compose.yml` with nothing else. Kong, GoTrue,
      PostgREST, Realtime, Storage and Studio are all running today and all unused (§4 says so
      outright), and shipping them means the teacher installs the Supabase CLI *as well as* Docker
      and the closet PC runs ~12 containers for the benefit of one.
      - Development is **completely unchanged**: `supabase start`, `db reset`, `seed.sql`, Studio
        and the pgTAP suite all stay exactly as they are. The CLI just does not travel to a school.
      - **The host port stays 54322 everywhere**, mapped `54322:5432`. The teacher never types it,
        so "5432 is conventional" buys nothing real, while a second value means every doc, script,
        comment and CI reference is a place to miss one. It also avoids colliding with whatever
        Postgres the school might already have.
      - Set a **generated** Postgres password in the compose environment at install time.
        `postgres:postgres` is correct for a localhost dev stack and wrong on a machine in a closet
        that students walk past. Store it in the install directory's `.env` with tight permissions
        and never show it to anybody.
- [ ] **Embed the migrations in the Go binary.** `//go:embed supabase/migrations/*.sql` plus a
      sequential runner and a `schema_migrations` table. The server applies whatever is pending at
      boot. Roughly a hundred lines, and the highest-leverage item in this file.
      - This is also the **upgrade story**: a school downloads a new binary and their database
        moves with it. Without it, every install *and every release forever* is a support ticket
        that starts "run these SQL files in this order".
      - The runner must read the same files the CLI reads, or dev and production schemas drift.
      - The restore path already checks a schema version (`--force` exists for a mismatch), so
        there is a version concept to hang this on.
- [ ] **Embed the web UI in the Go binary.** `//go:embed web-app/dist`, served at `/`. The desktop
      app already does exactly this (`desktop-app/main.go`), so the pattern is in the repo. Then
      the deliverable is **one binary**: API, UI and migrations.
      - Serving same-origin also makes the CORS allow-list irrelevant for an install, which deletes
        the single most confusing failure mode in the system (§13, 2026-09-15: a blocked preflight
        and a dead server are indistinguishable in the browser).
      - `packages/ui` hardcodes `DEFAULT_BASE_URL = http://127.0.0.1:8080`; same-origin serving
        means a relative base, which is also why `SERVER_ADDR` stops being load-bearing. The
        desktop host still has to be told explicitly.
      - Keep the Wails app as the nicer local window, not as a requirement.
- [ ] **A release workflow.** `.github/workflows/release.yml` on tag push (`v*`), a `go build`
      matrix producing `stockroom_windows_amd64.exe`, `stockroom_darwin_arm64`,
      `stockroom_darwin_amd64` and `stockroom_linux_amd64` — each with the UI and migrations
      embedded — plus `checksums.txt` and auto-generated notes. Without this a school needs Go.
- [ ] **`install.ps1` and `install.sh`.** Scripts, not an `.exe` (see §Decisions). Each one:
      - Check for Docker; install it via `winget install Docker.DockerDesktop` or a Homebrew cask
        when a package manager is present, and otherwise print the download link and what to click.
      - **If enabling WSL2 needs a restart, say so in one sentence and ask them to run the same
        command again afterwards.** Not a `RunOnce` resume — see §Cut.
      - Wait for the daemon, not the binary: poll `docker info` for up to two minutes with an
        honest "waiting for Docker to start (this takes about a minute the first time)".
      - Fetch the right binary for the platform from the latest release, write a starter `.env`
        with a generated database password, start Postgres, start the server, open `/setup`.
      - **On a second run, detect the existing install: back up first, then replace the binary and
        restart.** This is the upgrade path, and the backup is the only protection against a
        migration that goes wrong. It is also why "it broke, I'll run the installer again" is the
        thing that fixes it.
- [ ] **Register it as a service** — a Windows service (not a logon task), a launchd plist on
      macOS, a systemd unit on Linux — with **restart on crash and backoff**. **This is not
      optional.** The nightly backup lives inside the server, so "the server is running" and
      "backups happen" are the same fact, and today that depends on somebody having left a terminal
      window open. Offer an optional "restart now to check everything comes back on its own" at the
      end of the install; a school finding out on day forty that it never auto-started is a school
      that lost forty days of backups.

---

## Phase B — Make it usable by a school that isn't yours

- [ ] **Barcode generation.** *Previously filed under "Hardware, stickers and the physical setup",
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
      - **`GET /assets/{id}/barcode.png`**, rendered inline on the asset detail dialog and
        downloadable from the same endpoint — one endpoint, two features. The inline render is also
        the recovery path for the failure a school will actually hit: the sticker falls off, and
        they find the asset by name, read its serial and reprint.
      - **A printable ID-card layout** from the same generator, for schools whose cards carry no
        barcode or encode something other than the student number. Without it, scan login — the
        headline feature — does not work for them at all.
      - Sticker guidance alongside it: matte not glossy, where they survive on a lens barrel versus
        a body, laminate anything that goes outdoors.
      - A manufacturer's existing barcode is a valid serial. Plenty of gear already wears a unique
        barcode from the factory, which means **no sticker to print at all** for those units. Say
        this in the wizard; it is a real hour saved and nobody guesses it.
- [ ] **A fresh production database starts empty.** `seed.sql` becomes demo-only and says so at the
      top: it keeps the 63-node tree, the 12 assets, the kit and the two dev accounts, it loads on
      `supabase db reset` during development, and it **never** loads on an install. The only way
      into a fresh install is the wizard or the `.env` failsafe admin (§7), which is exactly what
      the failsafe is for and is already proven by `cmd/restore`'s zero-account path.
- [ ] **A category-tree import**, `POST /categories/import`, admin-only. Building an eight-Type
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
- [ ] **An asset CSV import**, `POST /assets/import`, columns `serial_number,name,category,model,
      status`, upserting by serial. A school with 300 items cannot type them into a dialog.
      `roster.go` is already the pattern: per-row errors collected with line numbers, the rest
      still landing. No column-mapping screen, no dry run, no undo token — see §Cut.
      - **Duplicate detection with a readable message.** A serial that already exists gets
        *"You already have an item with this serial: Canon 70-200mm (added March 3)"*, not a
        database constraint error and not a silent overwrite.
- [ ] **Bulk add N units of one model.** *"Add 12 × Canon LP-E6 battery"* generates `LPE6-001`
      through `LPE6-012`, **shows the list of serials before committing**, and offers the label
      sheet immediately. Linear stock — batteries, SD cards, bags, cables — is most of the unit
      count in a real department and all of the tedium. Let the prefix be edited and remember it per
      model, so next year's re-order continues the numbering instead of colliding with it.
- [ ] **A student-number format setting.** Digits-only, 1–32 today (`NormalizeStudentNumber`,
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
- [ ] **A seven-step first-run wizard** at `/setup`, reachable **only while `profiles` is empty** —
      an unauthenticated route that stops existing the moment there is an account, which is the only
      safe shape for it.
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
- [ ] **Example data, and a prompt to remove it.** Ship an `examples/` folder — a roster CSV, an
      asset CSV, two category trees — all obviously fake (`Example High School`, numbers starting
      `900`). These double as the fixtures for the install walkthrough, so the docs and the files
      cannot drift apart. Offer to load them at setup, so somebody can try the system with three
      cameras in it before spending an afternoon entering their inventory.
      - Seeded example rows get a **reserved UUID prefix** (`00000000-…-9xxx`) rather than an
        `is_example` column, so there is no migration and no flag that every real install carries
        forever. It is invisible in the UI and reads as magic to the next maintainer, which is why
        it is written down here.
      - **When real data is imported, prompt to delete the examples.** Fake cameras that outlive
        setup are worse than no demo at all, because the catalogue looks authoritative and is wrong.
- [ ] **Move `Catagories.md` to `examples/categories.media-department.md`** and stop calling it a
      source of truth. It becomes *an* example tree alongside at least one more — a theatre or a
      science department's — so the shape reads as a pattern rather than a spec.

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
- [ ] **`docs/INSTALL.md`** — the twenty-minute path, Windows first, a screenshot per step, no
      assumed knowledge. Cannot be written until Phase A exists; writing it against today's
      toolchain would produce a document that is obsolete on delivery. Document the PowerShell
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
