# Stockroom as a template: the work list

`TODO.md` tracks the build of Stockroom *for one school*. This file tracks turning that into
something **another school's teacher can install and run without reading any code**, and the GitHub
setup that distributes it.

Read `CLAUDE.md` for architecture and the decision log. Nothing here changes a product decision in
§2 — the scope stays what it is. This is packaging, configuration, documentation and distribution.

---

## The one sentence that defines "done"

> A media teacher at a school that has never heard of us downloads one file, follows one page of
> instructions, and is checking a camera out to a student within thirty minutes — without
> installing Go, Node, Docker Desktop or the Supabase CLI, without editing a file in a text editor,
> and without ever seeing a terminal error.

Every item below either serves that sentence or serves the person who has to maintain the thing
afterwards. Anything that serves neither is not on this list.

**Phase T6 is where that sentence is actually kept.** T5 turns Stockroom into one binary; T6 turns
that binary into a download a teacher can run and a wizard that walks them from an empty database
to a working checkout desk. If you only ever finish one phase in this file, finish that one — the
rest is quality, and T6 is whether anybody outside this school ever runs the software at all.

## Where we actually are

The code is more portable than the project is. Almost nothing in `internal/stockroom` or
`packages/ui` knows which school this is. Four things stand between here and a template:

| # | Blocker | Where it lives | Phase |
|---|---|---|---|
| 1 | The seed *is* one school's inventory, and ships a known admin login | `supabase/seed.sql`, `Catagories.md` | T2 |
| 2 | The name, tagline and vocabulary are hardcoded | `sign-in.svelte:297`, both `index.html` | T3 |
| 3 | Policy is constants — 7 days, digits-only IDs, 3 tree levels | `custody.go`, `password.go`, `categories_admin.go` | T4 |
| 4 | **Installing it means being a developer** | `scripts/dev.sh`, `README.md`, the whole toolchain | T5 + **T6** |

(4) is the project. (1)-(3) are each an afternoon. Do them in order anyway, because (4) is where the
motivation runs out and the first three are what make the demo look like *their* school.

---

## Phase T0: Decide the three things that are hard to undo

Everything else can be changed later. These three get harder the moment the repository is public.

- [ ] **Pick the licence, finally.** Currently AGPL-3.0, and you are the sole author, so today it
      costs one commit to change. After the first outside contribution it costs every contributor's
      written consent.
      - AGPL: anyone who forks it and runs it as a *network service* must publish their changes.
        Stockroom is localhost-only, so for a school running it as intended the obligation never
        triggers — but some district IT departments reject AGPL on sight, without reading it.
      - MIT / Apache-2.0: maximum adoption, no obligation, someone can sell a hosted version of
        your coursework and owe you nothing.
      - Recommendation: **Apache-2.0** if the goal is other schools using it; keep AGPL if the goal
        is that improvements come back. Decide by asking which outcome would annoy you more.
- [ ] **Pick the distribution shape.** Three options, and they are not equally good:
      - **A. Releases only** *(recommended)*. Schools never clone. They download a binary + a
        compose file from the Releases page. Upgrades are "download the new one". No git literacy
        required at any point.
      - **B. Template repository.** GitHub's *Use this template* button gives each school their own
        repo with no fork relationship. Good for a school that wants to customise; bad for
        upgrades, because a template copy **cannot merge your later changes** — there is no shared
        history to merge from.
      - **C. Both, split in two repos.** `stockroom` (source, public, where work happens) and
        `stockroom-deploy` (a six-file template: compose file, `.env.example`, installer, docs,
        pointing at a release tag). Upgrading is bumping one version string. This is the right
        answer if schools are expected to still be running it in two years.
      - Recommendation: **A now, C when a second school actually exists.** Do not build C for a
        hypothetical user.
- [ ] **Decide whether you will support it.** A public repo with an issue tracker is a promise.
      Write the promise down in `SECURITY.md` and the README — "best effort, no SLA, this is
      coursework" is a perfectly good promise, and an unwritten one becomes resentment on both
      sides. If the answer is "no support", say so and mark the repo *archived-friendly*: docs
      complete enough that nobody needs you.

---

## Phase T1: Make the repository safe to publish

The repo is **private** today. Git history is already clean — no `.env`, no `uploads/`, no roster
CSV and no backup zip has ever been committed (verified with `git log --diff-filter=A`). That is
the expensive problem already avoided. What remains is cheap and must still be done deliberately.

- [ ] **Check the working tree, not just history.** `uploads/` is gitignored but *exists* on your
      machine and may hold real student photographs; `.run-logs/`, `.cache/`, any `backup/` folder
      and any `.env` are the same. Confirm every one is ignored before the first `git add -A` on a
      public repo. One `git status --ignored` and a careful read.
- [ ] **Scan the history for secrets anyway.** `gitleaks detect --no-git=false` or
      `trufflehog git file://.` over the full history. Free, two minutes, and the one thing you
      cannot undo after publishing.
- [ ] **Search for real names and numbers in the docs.** `CLAUDE.md`, `TODO.md` and the design docs
      quote real data in places ("Admin Admin or Test User", student numbers, the department's
      inventory). Decide per instance whether it is sample data or somebody's actual name.
- [ ] **Remove or clearly label the school's own inventory.** `Catagories.md` is one department's
      equipment list. It is not secret, but as the *source of truth* it says "this repo is that
      school's". See T2.
- [ ] **Write `SECURITY.md`.** Private vulnerability reporting on (Settings → Security), an email
      address, and an honest threat model: localhost-only, no RLS, the DB superuser password is
      `postgres`, a backup archive contains a roster of working scan-login credentials. A school's
      IT will ask, and having answered already is the difference between a yes and a maybe.
- [ ] **Write the privacy note** (`docs/PRIVACY.md`): what personal data Stockroom stores (names,
      student numbers, photographs, a custody trail of who borrowed what and when), where it goes
      (one PC, plus whichever backup targets the school enables), how long it is kept (forever,
      currently — there is no retention policy or purge), and who can see it (any signed-in user
      sees the *current* holder's name; admins see everything). Name FERPA/GDPR without claiming
      compliance — say what the system does and let the school's data officer decide.
- [ ] **Decide what a school owes its students.** There is no "delete my data" flow beyond
      `DELETE /users/{id}`, which cascades the custody trail away entirely. That is either fine or
      a feature request; either way it belongs in the privacy note.

---

## Phase T2: Separate "this school's data" from "the software"

Right now `supabase/seed.sql` does two incompatible jobs: it is the demo, and it is the starting
inventory. A template needs those split.

- [ ] **`seed.sql` becomes demo-only, and says so at the top.** It keeps the 63-node tree, the 12
      assets, the kit and the two dev accounts. It loads on `supabase db reset` during development
      and **never** on an install.
- [ ] **A fresh production database starts empty** — no categories, no assets, no accounts. The
      only way in is the failsafe admin from `.env` (§7), which is exactly what it is for and is
      already proven by `cmd/restore`'s zero-account path.
- [ ] **Move `Catagories.md` to `examples/categories.media-department.md`** and stop calling it a
      source of truth. It becomes *an* example tree, alongside at least one more so that the shape
      reads as a pattern rather than a spec — a theatre department's, or a science department's.
- [ ] **Build a category-tree import.** `POST /categories/import`, admin-only, accepting the same
      indented-text shape the example files use (or a CSV of `type,category,model`). Building an
      eight-Type tree through *New category* is roughly sixty dialogs, and it is the **first** thing
      a new school does. It is also where they stop.
      - Must be idempotent (re-running adds what is missing, changes nothing else) because the
        first attempt will have a typo in it.
      - Must set `sort_order` from document order, the way the seed does — that column exists
        precisely because document order is not alphabetical order (§13, 2026-09-13).
      - Watch the constraint that bites: **`categories.name` is unique across the entire table**,
        not per parent. Two departments both having an "Accessories" node is an import failure with
        a confusing message unless the importer says so in plain words.
- [ ] **Build an asset CSV import.** `POST /assets/import`, columns
      `serial_number,name,category,model,status`, upserting by serial. A school with 300 items
      cannot type them into a dialog, and the roster importer (`roster.go`) is already the pattern
      to copy — per-row errors collected with line numbers, the rest still landing.
- [ ] **Ship an `examples/` folder** with a roster CSV, an asset CSV and two category trees, all
      obviously fake (`Example High School`, numbers starting `900`). These double as the fixtures
      for the install walkthrough, so the docs and the files cannot drift apart.
- [ ] **Add a demo-data button, or a `--demo` flag**, that loads the example set into a real
      install. Trying a system with three cameras in it is how somebody decides to adopt it; asking
      them to enter their inventory first to find out whether they like it is backwards.

---

## Phase T3: Identity — make it look like their school

- [ ] **Add `site_name`, `site_tagline` and `logo_path` to `app_settings`.** Same mechanism as the
      backup settings: `.env` seeds them on first boot, the admin panel owns them afterwards
      (§C.2's rule, unchanged). Admin → Settings → Appearance.
- [ ] **Replace the hardcoded strings.** `packages/ui/src/lib/screens/sign-in.svelte:297-298` reads
      `Stockroom` / `Media department equipment checkout`; `web-app/index.html` and
      `desktop-app/frontend/index.html` both hardcode the `<title>`.
- [ ] **Add `GET /branding`** — unauthenticated, tiny, cacheable — so the sign-in screen and the
      document title can render the school's name *before* anyone signs in. Sign-in is the one
      screen with no session, and it is the screen the name matters most on.
- [ ] **Logo upload** through the existing photo path (`storePhoto`, 10 MB cap, same extensions),
      landing at `uploads/branding/logo.<ext>`. One writer, like every other `photo_path` (§13).
- [ ] **Do not build a theme editor.** `tokens.css` is the design system (`design-system.md`), and
      one school wanting maroon is not worth a colour-picker plus the contrast bugs that follow.
      A logo and a name is what "ours" means. Document how to edit `tokens.css` for anyone who
      insists, and let them own the result.
- [ ] **Screenshots and a short screen recording** with the example data loaded, in the README.
      This is how adoption actually happens and it is worth an hour of care — sign-in, browse, a
      scan checking an item in, the overdue list.

---

## Phase T4: Policy — the constants that are somebody else's rules

These are decisions this department made that another department will make differently. Each one is
currently a constant in the code. Each becomes an `app_settings` column with today's value as the
default, so nothing changes for the existing install.

- [ ] **The 7-day checkout cap** (`custody.go`, `packages/ui/src/lib/due.ts`). Weekend-only lending,
      a semester-long loan for a senior project, a 24-hour turnaround on a shared microphone —
      every school answers this differently. `max_checkout_days`, default 7. **Both** layers read
      it, because the frontend cap and the server cap have to agree or the date picker offers a
      date the server refuses (§13, 2026-09-12: the cap is an exact instant, `now + N×24h`).
- [ ] **Student-number format** (`NormalizeStudentNumber`, `password.go`). Digits only, 1-32, today.
      Plenty of schools issue IDs like `AB12345`, and for them Stockroom currently does not work at
      all — the sign-in field filters their ID to nothing and the message never fires. Options, in
      order of effort: relax to `[A-Za-z0-9-]`, or add an `id_format` regex setting. **Whichever
      you choose, `sign-in.svelte`'s digit filter has to change with it**, and so does the
      scan-vs-typed comparison that runs the buffer through that same filter (§13, 2026-09-15) —
      this is the one item on this list that touches a genuinely subtle piece of code. Read that
      decision log entry before starting, and keep the six `scanner.test.ts` regressions green.
- [ ] **Session idle timeout** is already `SESSION_IDLE_MINUTES` in `.env`. Move it to
      `app_settings` so it stops being a file edit, like everything else did in Phase 7.
- [ ] **The overdue block.** Some departments will want a warning rather than a refusal.
      `overdue_blocks_checkout`, default true. The admin override stays either way.
- [ ] **The vocabulary.** "Student" is wrong for a staff-lending library or a community makerspace.
      A `member_noun` setting (student / member / staff / borrower) threaded through the UI strings
      is a couple of hours and doubles the number of places this fits. `CONTEXT.md` fixes this
      vocabulary deliberately, so if you do this, **update `CONTEXT.md` at the same time** — the
      code keeps saying `student_number` either way, and the glossary has to say why.
- [ ] **Scan threshold** — `SCAN_KEY_THRESHOLD_MS` in `packages/ui/src/lib/scanner.ts`, currently
      50 ms and still untuned against real hardware (§13, still open). A different school buys a
      different scanner. Expose it as a setting, keep Ctrl+Shift+D as the way to measure what to
      set it to, and document that procedure in the admin guide rather than in a code comment.
- [ ] **Leave the three-level category tree alone.** It is capped at depth 3 in
      `categories_admin.go` and that cap is load-bearing for the browse UI. A school with a
      four-level taxonomy can file assets at any node (ADR 0001). Write that down instead of
      building arbitrary depth.

---

## Phase T5: The install path — the actual work

*(Packaging. The install **experience** — dependency auto-install, the bootstrapper and the guided
first run — is [T6](#phase-t6-one-click--the-bootstrapper-and-the-guided-first-run), which depends
on everything here.)*

Today, running Stockroom requires Go, Node, Docker Desktop, the Supabase CLI, a git clone and a
terminal. Every one of those is a place a teacher stops. The target is **one downloaded file plus
Docker**, and ideally not even Docker.

- [ ] **Drop the Supabase CLI from deployment.** Keep it for development, where migrations, `db
      reset`, Studio and pgTAP are genuinely the good path. An install gets a `docker-compose.yml`
      with `postgres:17` and nothing else — Kong, GoTrue, PostgREST, Realtime and Storage are all
      running today and all unused (§4 says so outright). Fewer containers, less RAM, fewer things
      to explain, and a version of Postgres the school can point their own DBA at.
      - Change `DATABASE_URL`'s default port with care: **54322 is baked into the Supabase stack's
        config**, and a lot of docs, scripts and comments name it.
      - Set a real Postgres password in the compose file's env, generated at install time. The
        current `postgres:postgres` is correct for a localhost dev stack and wrong on a machine in
        a closet that students walk past.
- [ ] **Embed the migrations in the Go binary.** `//go:embed supabase/migrations/*.sql` plus a
      small sequential runner with a `schema_migrations` table (or `golang-migrate`). The server
      applies whatever is pending at boot.
      - This is also the **upgrade story**: a school downloads a new binary and their database
        moves with it. Without it, every release is a support ticket that starts "run these SQL
        files in this order".
      - The restore path already checks a schema version (`--force` exists for a mismatch), so
        there is a version concept to hang this on.
- [ ] **Embed the web UI in the Go binary.** `//go:embed web-app/dist`, served at `/`. The desktop
      app already does exactly this (`desktop-app/main.go`), so the pattern is in the repo. Then
      the deliverable is **one binary**: API, UI and migrations.
      - Serving the UI same-origin also makes the CORS allow-list irrelevant for an install, which
        deletes the single most confusing failure mode in the whole system (§13, 2026-09-15: a
        blocked preflight and a dead server are indistinguishable in the browser).
      - Keep the Wails app as the nicer local window, not as a requirement.
- [ ] **Make `DEFAULT_BASE_URL` configurable at runtime.** `packages/ui` hardcodes
      `http://127.0.0.1:8080`, which is why `SERVER_ADDR` is load-bearing and `dev.sh` warns about
      it. Same-origin serving mostly solves this (use a relative base), but the desktop host still
      needs to be told.
- [ ] **A first-run setup wizard** at `/setup`, reachable **only while `profiles` is empty** — an
      unauthenticated route that stops existing the moment there is an account, which is the only
      safe shape for it. It collects: the school/department name, the first admin account, and
      optionally a category tree, a roster and the demo data. It replaces "edit `.env`, restart,
      hope" as the front door.
      - Keep `ADMIN_STUDENT_NUMBER`/`ADMIN_PASSWORD` as the failsafe they already are. The wizard
        is the pleasant path; the failsafe is the floor. Do not collapse the two.
- [ ] **Installer scripts.** `install.ps1` (Windows first — the closet PC is Windows) and
      `install.sh` (macOS/Linux). Each: check for Docker and offer the download link if missing,
      fetch the right binary for the platform from the latest GitHub release, write a starter
      `.env`, start Postgres, start the server, open the browser at `/setup`.
- [ ] **Register it as a service** so it survives a reboot — Task Scheduler *at logon* or a Windows
      service on Windows, a launchd plist on macOS. **This is not optional.** The nightly backup is
      a goroutine inside the server (§11), so "the server is running" and "backups happen" are the
      same fact, and today that depends on somebody having left a terminal window open. A closet PC
      gets unplugged, and the boot catch-up only helps if the server comes back up by itself.
- [ ] **A real "is it working" surface for the person at the machine.** A tray icon, or a status
      page at `/` when the server is up but the database is not. Today a failed start is a line in
      a terminal that nobody is looking at.
- [ ] **Uninstall instructions**, and a *move to a different PC* procedure — back up, install
      elsewhere, restore. The restore path already supports this exactly; it just has to be written
      down as a supported operation rather than a disaster one.
- [ ] **Decide about Docker.** The honest alternatives are: (a) keep Docker, simplest to build,
      one more install for the school; (b) ship an embedded database (SQLite would mean rewriting
      every query and losing the pgTAP suite — not worth it); (c) bundle Postgres binaries. **(a)
      is the right answer**, but say so explicitly in the docs with the reason, because "why do I
      need Docker" is the first question and a confident answer is worth more than a clever one.

---

## Phase T6: One-click — the bootstrapper and the guided first run

T5 makes Stockroom *one binary*. This phase makes it **one download that a teacher can run**, and
then walks that teacher from an empty database to a working checkout desk without them ever
guessing what to do next.

Treat this as the most important phase in the file. Everything else is quality; this is whether
anyone else ever runs the software at all. Every item here exists because of a specific moment
where a real person would stop, close the window, and go back to the clipboard.

### The honest ceiling

**"One click" is a goal, not a claim.** Some things genuinely cannot be automated away, and
pretending otherwise produces an installer that lies and then fails:

- Installing Docker needs administrator rights, and on Windows it may need a **reboot** to enable
  WSL2 or virtualization.
- Virtualization is sometimes disabled in BIOS, which no installer can change.
- An unsigned `.exe` trips SmartScreen; a signed one costs money every year.
- A school's filtered network may block the download outright.

So the real target is: **two clicks, one administrator password, and at most one reboot that the
installer resumes itself after.** Every unavoidable stop is stated in advance, in plain English,
with the exact button to press — and never as an error the user has to interpret.

Write that target at the top of the installer's own README. An installer that is honest about one
reboot is trusted; one that promises magic and then shows a PowerShell stack trace is uninstalled.

### T6.1 — One file, and it is a bootstrapper

- [ ] **One download per platform.** `StockroomSetup.exe` (Windows x64), `Stockroom.pkg` (macOS,
      universal), `install.sh` (Linux). Nothing else on the download page — a page offering four
      files with different names is a page where somebody picks the wrong one.
- [ ] **It is a bootstrapper, not the app.** A few hundred KB that detects the machine, fetches the
      right release asset, verifies its checksum, and installs. Keeps the download page simple and
      makes "upgrade" the same code path as "install".
- [ ] **Detect OS and architecture, and refuse politely when unsupported** — 32-bit Windows, ARM
      Windows, macOS below the minimum. A clear "Stockroom needs 64-bit Windows 10 or later; this
      machine is …" beats a half-finished install every time.
- [ ] **Idempotent by construction.** Running it a second time detects the existing install and
      offers **Update · Repair · Uninstall** rather than making a second copy. The single most
      common real-world action is "it broke, I'll run the installer again", and that must be the
      thing that fixes it.
- [ ] **An offline bundle.** A second, larger download containing the binary *and* the Postgres
      image tarball (`docker save`), for a school whose network blocks Docker Hub or whose closet
      PC has no internet at all. `docker load` from a USB stick is a supported path, not a hack.
- [ ] **An install manifest.** Record everything the installer put on the machine, so the
      uninstaller removes exactly that and nothing a user had already.

### T6.2 — Make sure everything is installed, automatically

The rule for every dependency: **detect → install → verify it actually works → explain in one
sentence if it cannot.** A version string is not verification.

- [ ] **Docker.** Install via `winget install Docker.DockerDesktop` on Windows, Homebrew cask or
      the `.dmg` on macOS, the distro package on Linux. If a package manager is missing, fall back
      to downloading the official installer and running it silently.
- [ ] **Handle the reboot, do not hand it to the user.** Enabling WSL2 or Hyper-V on Windows can
      require a restart. Register a `RunOnce` entry so the installer **resumes itself** after the
      reboot and continues where it stopped, with a full-screen "Stockroom is finishing its setup"
      on the way back up. A user who is told "restart and run the installer again" has been given a
      chore they may not come back from.
- [ ] **Detect virtualization disabled in BIOS**, and stop with the one thing that helps: a short,
      copy-pasteable paragraph they can forward to whoever manages the school's machines, naming the
      setting (Intel VT-x / AMD-V) and why it is needed. This is the one failure an installer truly
      cannot fix, so make it two minutes of somebody else's time instead of an afternoon of theirs.
- [ ] **Verify the daemon, not the binary.** `docker info` must succeed. Docker Desktop takes up to
      a minute to become ready after install or boot, so poll for up to two minutes with an honest
      "waiting for Docker to start (this takes about a minute the first time)" — never fail at
      second three.
- [ ] **Pull the Postgres image with visible progress.** It is a few hundred megabytes on a school
      connection, which is where a silent installer looks frozen. Resume on failure; retry twice.
- [ ] **Never require Go, Node, the Supabase CLI or git.** If the bootstrapper needs any of them,
      T5 is not finished. Those four are the entire difference between today's install and this one.
- [ ] **`rclone` is installed on demand, not up front** — only when somebody chooses one of the two
      features that needs it, installed by a button on that screen. Those are Google Drive backups
      and the sign-in photo wall, which reads its photographs from a Drive folder; either one alone
      is enough to need it, and the wall's screen must offer the same button rather than assume the
      backup screen was visited first. A dependency for an optional feature must not be part of the
      first five minutes.
- [ ] **Antivirus and SmartScreen, handled in advance.** Document the SmartScreen prompt with a
      screenshot and the exact words on the button ("More info" → "Run anyway"), and offer to add
      the install folder as an exclusion. A teacher who sees "Windows protected your PC" and no
      explanation concludes, reasonably, that they have downloaded malware.
- [ ] **Corporate proxies.** Honour `HTTP_PROXY`/`HTTPS_PROXY`, and if a download fails, say
      *"Stockroom could not reach the download server. If your school uses a web filter, this is
      usually why"* rather than printing a TLS error.
- [ ] **A generated database password.** Do not ship `postgres:postgres` to a machine in a closet.
      Generate one at install time, store it in the install directory's `.env` with tight file
      permissions, and never show it to anybody.

### T6.3 — A preflight doctor that runs before anything is written

- [ ] **`stockroom doctor`** — run automatically by the installer *before* it changes anything, and
      available afterwards from a desktop shortcut, from the admin panel, and on the command line.
      Each check reports **PASS / WARN / FAIL** with a sentence saying what to do about it:

| Check | Why it is on the list |
|---|---|
| OS version and architecture | Refuse early, not halfway |
| Administrator rights | The install needs them; say so before asking for a password |
| Virtualization available | The one failure nobody can automate |
| Docker daemon responding | Not the binary — the daemon |
| Disk space, with a projection | The photo mirror only ever grows (§11); warn at install, not in year two |
| Ports 8080 and 5432 free, **and what is holding them** | "Port 8080 is in use by *Skype*" is actionable; "bind: address already in use" is not |
| Write permission on the install and data directories | OneDrive-redirected `Documents` folders break this in ways nobody expects |
| **System clock accuracy** | Due dates and session expiry are wall-clock. A PC with a dead CMOS battery and a 2019 clock makes every item permanently overdue, and nothing on any screen would explain why |
| A HID keyboard-wedge device attached | A gentle "no scanner detected — you can still use Stockroom by typing" |
| Power settings: sleep and hibernate off | The nightly backup is a goroutine in the running server. A sleeping PC silently stops backing up |

- [ ] **Offer to fix what it can.** Sleep settings, firewall rule, antivirus exclusion, folder
      permissions — each with a *Fix this for me* button and a plain description of what it changes.
- [ ] **A copyable support bundle** from the same screen: doctor output, the install log, the server
      log tail and the settings **with every secret stripped**, zipped, with the path shown. This is
      what somebody attaches to an issue instead of describing the problem from memory.

### T6.4 — It starts by itself, and keeps starting

- [ ] **Register as a real service** (Windows service, not a logon task; launchd on macOS; systemd
      unit on Linux), so it runs before anybody signs in and survives a locked screen. Cross-refers
      T5; it belongs here because a "one-click install" that stops working after the first reboot
      has not installed anything.
- [ ] **Restart on crash**, with backoff, and log why.
- [ ] **Prove it during install.** Offer an optional "restart now to make sure everything comes back
      on its own" at the end. A school finding out on day forty that it never auto-started is a
      school that lost forty days of backups.
- [ ] **Desktop shortcut, Start-menu entry and a tray icon.** The tray icon is green when the server
      answers `/health`, amber when the database is down, red when the server is not running — and
      clicking it opens the doctor. Today a failed start is a line in a terminal nobody is watching.
- [ ] **An in-app updater.** Check for a newer release, show what changed, and on one button: take a
      backup first, stop the service, swap the binary, apply migrations, start, verify `/health`,
      and **roll back automatically if it does not come up**. Never an update that can leave the
      machine with no working checkout desk in the morning.
- [ ] **An uninstaller that asks about the data.** *"Remove Stockroom but keep your equipment and
      borrowing records?"* — and if they say remove everything, take a final backup first and tell
      them exactly where it is. Deleting a department's entire lending history on one click is the
      one place a confirmation is not friction.

### T6.5 — The guided first run

The installer's last act is to open a browser at `/setup`. That route exists **only while there are
no accounts**, and stops existing the moment the first admin is created — a wizard that can be
reached later is a way to reconfigure a live system by accident.

Design rules for every step:

- **Each step saves as it completes.** Closing the laptop and coming back tomorrow resumes where it
  stopped. Nothing is held in memory across twelve screens.
- **Every step is skippable with *I'll do this later*, except creating the admin account.** A person
  who cannot skip is a person who will enter rubbish to get past a screen.
- **Plain words only.** No "instance", "schema", "migration", "commit", "transaction", "seed". The
  wizard never names a file path the user did not choose.
- **A progress rail** showing all twelve steps, so the end is visible from the start.
- **A *Why?* link on every non-obvious field**, expanding into two sentences. Nobody reads a manual;
  everybody clicks a question mark next to the thing confusing them.

The twelve steps, in order:

| # | Step | Skippable |
|---|---|---|
| 1 | Welcome — what is about to happen, and how long | — |
| 2 | Name your department | yes (defaults to "Stockroom") |
| 3 | **Create your admin account** | **no** |
| 4 | **The safety net** — the failsafe admin password | **no** |
| 5 | Lending rules — how long, and what happens when something is late | yes |
| 6 | Build your catalogue — the category tree | yes |
| 7 | **Add your equipment** | yes |
| 8 | **Add your people** | yes |
| 9 | Print your barcode stickers | yes |
| 10 | Test your scanner | yes |
| 11 | Turn on backups | yes, with a warning |
| 12 | You're ready — the cheat sheet | — |

- [ ] **Write the copy before building the screens.** Draft text for the load-bearing ones is in
      T6.8 below; it is in this file rather than in a design doc because the words *are* the
      feature. A wizard with correct logic and vague prompts fails exactly as hard as a broken one.

### T6.6 — Adding equipment: four ways in, because one is never enough

This is where adoption is won or lost. A department with 300 items and only a *New asset* dialog
will not finish, and a half-entered catalogue is worse than a clipboard because it looks
authoritative and is wrong.

- [ ] **Scan-to-create — the one that changes everything.** In the admin catalogue, scanning a
      barcode Stockroom does not recognise currently does nothing useful. Instead: *"That barcode
      isn't in Stockroom yet — add it now?"* opens a two-field form with the serial already filled
      in from the scan. Name it, pick a category (the last one used is pre-selected, because
      inventory is entered a shelf at a time), press Enter, scan the next thing.
      - Inventory entry becomes: **pick up item, stick label on, scan, type name.** Roughly eight
        seconds per unit, using the scanner already plugged in, with no keyboard-to-mouse switching
        and no dialog to close. It turns two afternoons into ninety minutes.
      - It is also how a department stays accurate afterwards, because adding the camera bought in
        March costs eight seconds rather than a trip to the admin panel.
      - **Only in the admin catalogue, and only for an admin.** Everywhere else, an unknown barcode
        must keep saying "unknown item" — a student scanning a chocolate bar must not create an
        asset.
- [ ] **A manufacturer's existing barcode is a valid serial.** Plenty of gear already wears a unique
      barcode from the factory. Scanning it into scan-to-create means **no sticker to print at all**
      for those units. Say this in the wizard; it is a genuine hour saved and nobody guesses it.
- [ ] **Bulk add N units of one model.** *"Add 12 × Canon LP-E6 battery"* generates `LPE6-001`
      through `LPE6-012`, **shows the list of serials before committing**, and offers the label
      sheet immediately. Linear stock — batteries, SD cards, bags, cables — is most of the unit
      count in a real department and all of the tedium.
      - Let the prefix be edited and remember it per model, so next year's re-order continues the
        numbering instead of colliding with it.
- [ ] **CSV import with a column-mapping screen.** Upload, then a table showing the first five rows
      with a dropdown above each column — *this column is → Serial number / Name / Category / Skip*.
      Nobody should have to rename spreadsheet headings to match documentation.
      - **A dry run before anything is written**: *"This will add 247 items, update 12, and skip 3
        rows that have problems"*, with the three problems listed by line number and what is wrong
        with each. Then, and only then, a Import button.
      - **An undo token, valid for 24 hours.** *"Undo this import"* on the success screen and in the
        admin panel. The second import attempt is always the good one, and without an undo the first
        one's mess has to be cleaned by hand.
      - A downloadable **template CSV** with the right headers and two example rows, from the same
        screen. Half of all import problems are solved by giving somebody the file to fill in.
- [ ] **One at a time**, which already exists, kept for the camera bought yesterday.
- [ ] **Duplicate detection on every path.** A serial that already exists is a clear *"You already
      have an item with this serial: Canon 70-200mm (added March 3)"* — not a database constraint
      error, and not a silent overwrite.
- [ ] **Photos without friction.** A webcam capture button on the asset form (the closet PC has a
      camera or can have one), plus drag-and-drop. A photo per item is what makes the browse screen
      usable by a student who knows what the thing looks like but not what it is called.
- [ ] **The guidance the wizard shows while they do it**, in a sidebar, not a manual:
      *one asset is one physical object you can hand to somebody* · *two identical cameras are two
      entries, because you need to know which one came back* · *every battery gets its own entry and
      its own sticker* · *the serial is whatever is on the sticker — it only has to be unique* ·
      *`unavailable` means broken, lost or retired: it stays in your records but nobody can borrow
      it.*

### T6.7 — Adding people: the same care, different data

- [ ] **Roster CSV with the same mapping-and-dry-run screen as items.** One component, both jobs, so
      a fix to one fixes both. Columns map, problems list by line number, undo for 24 hours.
- [ ] **A path for "I don't have a CSV file".** Paste a list of names, or add people one at a time.
      Many departments have a printed list and nothing else, and telling them to go and produce a
      CSV is where they stop.
- [ ] **A path for "our students don't have ID numbers".** Offer to assign numbers from a range
      (`900001` upward), then print ID cards with the matching barcodes from the same label
      generator the stickers use. This makes Stockroom work for a club, a makerspace or a small
      school that has no student ID system at all — a genuinely larger audience.
- [ ] **Explain what the student will experience, on the screen where the admin imports them**, and
      give them the text to send:
      > Everybody you just added can sign in by scanning their ID card. The first time they do,
      > Stockroom asks them to choose a password — that takes about ten seconds and only happens
      > once. Until then they cannot sign in by typing their number, which stops anybody borrowing
      > under somebody else's name.
      With a **Copy announcement text** button producing something the teacher can paste into an
      email or read to a class. The most common adoption failure is a system that works and a group
      of students who were never told how to use it.
- [ ] **Promote admins inside the wizard**, on the same screen. The second admin is the one who
      covers for the first being off sick, and asking for them at setup time is how it gets done.
- [ ] **Photos are optional and explained.** They make the "who has this" screen instantly readable,
      and they are personal data the school may have rules about. Say both sentences; let them
      choose.

### T6.8 — The words themselves

Draft copy for the screens that carry weight. Refine it against a real reader, but **do not ship
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

**Step 3 — Create your admin account**

> **Create your account.**
> You'll use this to add equipment, manage people, and see who has what.
> **Your ID number** — the number on your school ID card. If your card has a barcode, scan it now
> and we'll fill this in. *[ field ]*
> **Your name** *[ first ]* *[ last ]*
> **A password** — you'll only need this when you type your number instead of scanning it. At least
> 8 characters. *[ field with a reveal button ]*
> *Why a password if I can scan?* → Scanning is the fast way in at the counter. Typing your number
> needs a password, so somebody who simply knows your number can't sign in as you.

**Step 4 — The safety net** *(cannot be skipped)*

> **One thing to write down.**
> This is a spare administrator account that Stockroom re-creates every time it starts. If a screen
> ever breaks, or the last admin account is deleted by accident, this is how you get back in.
> **Spare ID number** *[ 900000 ]* **Spare password** *[ generated, revealable ]*
> **Save this somewhere that isn't this computer** — a password manager, or a sealed envelope in a
> drawer. If the computer fails and you have to set Stockroom up again, this is what gets you back
> into your restored records.
> ☐ I have saved these somewhere safe *(required to continue)*

**Step 5 — Lending rules**

> **How does your department lend equipment?**
> **How long can somebody keep something?** *[ 7 ] days* — students pick a return date when they
> check out, and they cannot pick one further away than this.
> **When something is late:** ( ) Stop that person borrowing anything else until it comes back
> *(recommended)* ( ) Just show a warning
> You can change both of these later in Settings.

**Step 7 — Add your equipment**

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

**Step 10 — Test your scanner**

> **Let's check your scanner works.**
> Scan any barcode — an item, an ID card, a book, anything. *[ live field ]*
> ✅ *We saw `T7IBAT-001` and it arrived in 34 ms, which is well within the range Stockroom reads as
> a scan. Your scanner is set up correctly.*
> ⚠️ *We saw the characters but no Enter at the end. Your scanner needs its "add Enter after scan"
> setting turned on — it's usually a barcode printed in the scanner's manual. [What this means]*
> ❌ *Nothing arrived. Check the scanner is plugged in and that its light comes on when you press
> the trigger. [More help]*

**Step 11 — Backups**

> **Protect your records.**
> Stockroom backs itself up every night, automatically. Choose where those backups go.
> **On this computer** *[ folder picker, with a sensible default already filled in ]* — takes a
> minute to set up and covers most problems.
> **Also send them somewhere else** *(recommended, optional)* — Google Drive or GitHub, so a failed
> hard drive doesn't take your records with it. You can set this up now or later in Settings.
> *[ Run a backup now and show me it worked ]* ← do this. It is the only proof that matters.
> ⚠️ If you skip this entirely, Stockroom will remind everybody who signs in until it's set up.

**Step 12 — Done**

> **You're ready.**
> *(a summary: 247 items · 31 people · backups on)*
> **[ Print the student instructions ]** — one page for the wall above the computer.
> **[ Print your barcode stickers ]** — for the items that need one.
> **[ Open Stockroom ]**
> Next: scan your own ID card at the sign-in screen and check something out, so you've seen what
> your students will see.

### T6.9 — How it talks when things go wrong

- [ ] **No stack traces, no error codes, ever, on a screen a teacher can reach.** Every failure gets
      three things: what happened in one sentence, what to do about it, and a *Copy details* button
      for an issue report.
- [ ] **Name the cause when it is knowable.** "Port 8080 is being used by another program (Skype)"
      instead of "bind: address already in use". The doctor already knows which process it is.
- [ ] **`503 Not configured` must never reach a first-run user.** That status exists because the
      admin reading it is the person who fixes it — but in a wizard the right behaviour is to offer
      the fix, not report the state.
- [ ] **One log file, one place**, with a *Show me the log* button. Anywhere the installer or the
      app writes something a human might need, it says where it wrote it.
- [ ] **No telemetry. State it in the wizard.** *"Stockroom doesn't send any information about you,
      your equipment or your students anywhere. The only thing that ever leaves this computer is the
      backup, to the place you choose."* A school will ask, and a confident sentence in the product
      is worth more than a paragraph in a privacy policy.

### T6.10 — What stays deliberately hard

Friction in the right place is not a defect. These do not get smoothed:

- [ ] **The failsafe password must be acknowledged, not auto-hidden.** Generated for them, shown
      once, with a required tick-box. An unrecorded recovery credential is not a recovery path.
- [ ] **Restoring still requires typing `RESTORE`.** It replaces every record in the database.
- [ ] **Deleting a photo generation, forcing a restore past a refusal, deleting a user** — all
      unchanged. The wizard makes *starting* easy; it does not make *destroying* easy.
- [ ] **The seeded demo accounts are never installed by the installer.** They exist for development
      only, and their credentials are published in a public README.

### T6.11 — How we know it worked

- [ ] **The test is a person, not a checklist.** A clean Windows 11 machine with nothing installed,
      a shoebox of twenty items, a roster CSV of thirty names, a scanner still in its packaging, and
      a teacher who has never seen Stockroom. Hand them the download link and **say nothing else.**
- [ ] **Three numbers, recorded:** minutes from download to first successful checkout; the number of
      times they asked for help (**target: zero**); the number of times they saw something they
      could not act on (**target: zero**). Every one of those moments is a bug filed against this
      phase.
- [ ] **It has to pass twice, with two different people**, because the first person's confusions get
      fixed and the second person finds the ones that fix introduced.
- [ ] **Then run it on a machine behind a school's actual network filter**, which is the environment
      none of the above simulates and the one where it will really be used.

---

## Phase T7: Hardware, stickers and the physical setup

A school does not adopt software, it adopts a process. The parts that are not code:

- [ ] **A scanner buying guide** (`docs/HARDWARE.md`): what "USB HID keyboard-wedge" means in shop
      terms, two or three specific models that are known to work with prices, what to avoid
      (anything needing vendor software, anything Bluetooth-pairing-only, 2D imagers configured for
      a prefix), and how to verify one in five minutes with Ctrl+Shift+D before the return window
      closes. **Blocked on buying one** — this is still open in `CLAUDE.md` §13 and nothing can
      close it but hardware.
- [ ] **Barcode sticker generation.** This is the second adoption cliff after data entry, and it is
      currently not addressed at all. A school with 300 items needs a printable sheet, not a
      barcode website and an afternoon. `GET /assets/labels.pdf?ids=...` producing Code 128 of the
      serial on an Avery 5160/L7160 grid, with the asset name under it. Contained, self-hosted, no
      internet, and it makes the difference between a system in use and a system installed.
      - Include the sticker guidance too: matte, not glossy; where they survive on a lens barrel
        versus a body; laminate for anything that goes outdoors.
- [ ] **A printable ID-card layout** for schools whose existing cards have no barcode, or whose
      barcode encodes something other than the student number. Same generator, different template.
- [ ] **A physical setup page**: where the PC goes, the scanner on a short cable at counter height,
      a monitor a student can read standing up, and the fact that the machine must stay signed in
      and awake — sleep settings are a real failure mode for a nightly backup goroutine.
- [ ] **Label the failure a school will actually hit**: the sticker falls off. Document the
      recovery (find the asset by name in the admin panel, read its serial, reprint) somewhere a
      panicking person will find it.

---

## Phase T8: Documentation, rewritten for people who are not you

The current docs are excellent and are written for a maintainer. A template needs a second set.

- [ ] **`README.md` — done, rewritten for both audiences.** Setup first, development last, with a
      deep FAQ. Keep it honest about what is not built yet.
- [ ] **`docs/INSTALL.md`** — the twenty-minute path, Windows first, with a screenshot per step and
      no assumed knowledge. Cannot be written until T5 exists, because writing it against today's
      toolchain would produce a document that is obsolete on delivery.
- [ ] **`docs/ADMIN-GUIDE.md`** — the teacher's manual. Adding an asset. Importing a roster from the
      SIS export. Building the category tree. Printing stickers. What to do when a student leaves
      with a camera. How to read the overdue list. How to hand the system over to next year's
      teacher. `docs/BACKUP-SETUP.md` is already written at exactly this register — match it.
- [ ] **`docs/STUDENT-GUIDE.md`** — one page, printable, sticky-taped to the wall above the PC.
      Scan your card. Find your gear. Add to cart. Pick a date. Scan it back when you return it.
- [ ] **`docs/FAQ.md`** or the README's FAQ section, kept as the single place questions accumulate.
      Every real question a real school asks goes in it, in their words, not yours.
- [ ] **`docs/UPGRADING.md`** — how to move from one version to the next, what a migration does,
      and the instruction to back up first. Empty until there are two versions, and then urgent.
- [ ] **A one-page PDF for the person who signs the purchase order** — what it is, what it costs
      (a PC they have, a $40 scanner, labels), what it replaces, what it does not do. Adoption
      decisions are made by someone who will never read the README.
- [ ] **Keep `CLAUDE.md` §13 as it is.** It is the best thing in the repository and it is not
      documentation for schools; it is documentation for whoever maintains this next, including
      future you. Do not dilute it into a user guide.
- [ ] **Reconcile the docs that now disagree.** After T2-T5, these say things that are no longer
      true: `CLAUDE.md` §3/§9 (the deployment model and the setup steps), §6.2 (`Catagories.md` as
      the seed's source), `TODO.md`'s framing, `CONTEXT.md` ("Not configured" says the admin edits
      `.env`), `CI.md`, and `docs/BACKUP-SETUP.md` §1 (folder paths on a machine that now has an
      installer). Doing this as one pass at the end is a mistake — amend each as you land its
      phase, the way the header of `CLAUDE.md` already tracks amendments.

---

## Phase T9: GitHub setup

In order. Several of these are easier before the repo is public than after.

- [ ] **T0 first.** Do not make it public before the licence decision and T1's scan.
- [ ] **Fill in the repository description and topics.** `school`, `inventory`, `checkout`,
      `barcode`, `education`, `equipment`, `go`, `svelte`, `self-hosted`. Topics are the only
      discovery mechanism GitHub gives you and they cost thirty seconds.
- [ ] **Flip to public** (Settings → General → Danger Zone).
- [ ] **Branch protection on `main`**: require a PR, require the `tests` check, no direct pushes.
      You already work on `testing` and PR into `main` — this makes the habit structural, and stops
      a drive-by commit from a future collaborator. Keep the docs-only CI skip (`CI.md`) so a
      README fix is not a ten-minute wait.
- [ ] **Extend CI to a matrix.** `tests.yml` proves it on one runner. A template must prove
      `windows-latest` green, because Windows *is* the deployment target and `dev.ps1` has never
      been run on real Windows hardware (§9, still open). This is a correctness gap, not a polish
      item — it is the single most likely thing to break for the first school that tries.
- [ ] **A release workflow.** `.github/workflows/release.yml` on tag push (`v*`), GoReleaser or a
      `go build` matrix, producing `stockroom_windows_amd64.exe`, `stockroom_darwin_arm64`,
      `stockroom_darwin_amd64`, `stockroom_linux_amd64`, each with the UI and migrations embedded,
      plus `checksums.txt` and auto-generated release notes.
      - Windows SmartScreen will warn on an unsigned `.exe`. Code signing costs money; the
        alternative is a screenshot in `INSTALL.md` showing exactly which button to press. Say it
        in the docs rather than letting a teacher meet it cold and conclude they downloaded malware.
- [ ] **Tag `v1.0.0` only after T10 passes.** A 1.0 that does not install is worse than a 0.9.
- [ ] **Issue templates** (`.github/ISSUE_TEMPLATE/`): `bug.yml`, `feature.yml`, and
      **`install-help.yml`** — which will be the majority of what arrives. Auto-apply the labels
      `docs/agents/triage-labels.md` already defines (`needs-triage`, `needs-info`,
      `ready-for-agent`, `ready-for-human`, `wontfix`) so the existing agent skill keeps working
      unchanged. Add `config.yml` pointing questions at Discussions instead of Issues.
- [ ] **Turn on Discussions, leave the Wiki off.** Schools ask questions that are not bugs, and
      wikis rot in a way that a repository file does not.
- [ ] **`CONTRIBUTING.md`** — pointing at `dev.sh deps`, `dev.sh test`, the pre-commit hook, and
      the §13 house rule (a decision that cost an argument gets a line with the alternative that
      lost). **Say the house rules explicitly**, because they are unusual and a contributor who
      does not know them will produce a PR you have to reject.
- [ ] **`CODE_OF_CONDUCT.md`** (Contributor Covenant) and **`CODEOWNERS`** (you). Both are one-click
      from GitHub's UI.
- [ ] **Enable private vulnerability reporting** and Dependabot alerts. Dependabot *PRs* are worth
      enabling only if you will merge them; an ignored bot is noise that teaches you to ignore the
      tab.
- [ ] **Pin a "Start here" Discussion** with the install link, the demo video and the promise from
      T0 about how much support to expect.
- [ ] **Decide on the deploy repo** (T0 option C). Only build it when a second school exists.

---

## Phase T10: Prove it, on a machine that is not yours

*(T6.11 is the same discipline aimed at the installer alone. This phase is the whole product,
including the parts a wizard never touches — real backup targets, a reboot, a pilot term.)*

This is the acceptance test, and it is the same discipline that found three real defects in the
backup system: doing it for real rather than reasoning about it (§13, 2026-09-18).

- [ ] **A clean VM, or a borrowed laptop.** No Go, no Node, no Docker pre-installed, no repository
      clone. Follow `INSTALL.md` word for word, and **change nothing while doing it** — every time
      you reach for the source, that is a bug in the docs, and it goes in a list.
- [ ] **Time it.** If it takes more than thirty minutes, the number goes in the README and in this
      list until it does not.
- [ ] **Do a real checkout**: import a roster, import a category tree, import assets, print a
      sticker sheet, scan a card, scan an item, check it out, check it back in.
- [ ] **Configure a backup to a real Google Drive and a real private GitHub repo**, then restore
      from each. Drive has pushed to a real account since 2026-09-21; GitHub is still unexercised
      (§13, still open), and a template cannot ship a backup system whose second target has only
      ever talked to a fake.
- [ ] **Reboot the machine.** Confirm the server comes back by itself and the backup still fires.
- [ ] **Hand it to somebody who has not seen it** — a teacher, a parent, another student — and
      watch without helping. Write down every place they pause. That list is the next phase.
- [ ] **Carried over from [`TODO.md`](TODO.md), which is otherwise closed** — three items no
      backend work would have closed. The first two wait on hardware and credentials; the third
      waits on this track's own install and configuration work, so it is a blocker with a fix in
      this file rather than something to wait out:
      - [ ] Buy a scanner and tune `SCAN_KEY_THRESHOLD_MS` against it. Everything about scanning is
            currently a reasoned 50 ms guess that has never met hardware.
      - [ ] Run both backup targets at once, break one, and confirm the other still succeeds and
            the status names which failed.
      - [ ] A configuration-only run-through from a fresh install, without opening a text editor.
            This is the same test as T6.11, from the other end: T6.11 asks whether a stranger can
            install it, this asks whether *you* can set it up without touching a file.
- [ ] **A pilot school before a wide release.** One real school, one real department, one term.
      Everything above is theory until then.

---

## Open questions

- **Who owns this after you graduate?** A template implies maintenance, and coursework implies an
  end date. Whether the answer is "nobody, it is archived and complete" or "the next CS student",
  it belongs in the README before anyone depends on it.
- **How does a school get their data *out*?** Backup CSVs are the answer today and it is a decent
  one, but "we want to stop using Stockroom" should be a documented path, not a rescue operation.
  It is also the thing that makes adopting it low-risk.
- **Multi-department in one school?** Two departments, one PC, one database, separate inventories.
  Currently impossible and arguably out of scope — but it is what a school will ask for second.
- **How much of Phase 7's backup complexity should a template default to?** Local-only backups
  work with zero configuration; Drive and GitHub each need an account and a token. The default
  should be "on, local, working" with the off-site targets clearly optional, and the first-run
  wizard should set a folder rather than leaving it unconfigured — a 503 on the backup screen is a
  bad first impression of a system whose main selling point is that it does not lose your data.
