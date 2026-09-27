# Roadmap

Open work only. The reasoning behind finished work lives in [`docs/decisions.md`](docs/decisions.md). When you finish a task, delete it from this file and add a dated entry there saying what was decided and why.

## The goal

Stockroom running in production on the school's closet PC. Production means all of this:

- It installs with one command on a machine that has nothing on it.
- It starts on its own after a power cut, with nobody logged in.
- It backs up every night to Google Drive.
- Students use it for a term without the developer in the room.

## Platform order

1. **Linux** (Debian 12, Ubuntu 24.04). This is the main target and the closet PC's expected OS.
2. **macOS**, through Homebrew.
3. **Windows through WSL 2**, running the Linux package inside Ubuntu. This is the last main-track section.

There is no native Windows installer, service or package, and none is planned.

## Where it ends up

```sh
# Linux, and Ubuntu under WSL
curl -fsSL https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.sh | sudo bash

# macOS
brew install kathir-d/stockroom/stockroom && stockroom setup
```

`get.sh` installs a `.deb` that pulls in PostgreSQL and rclone, then runs `stockroom setup`. Setup creates the database, writes the config, installs and starts the service, and opens the web setup wizard that already exists (`packages/ui/src/lib/screens/setup.svelte`). Upgrading means installing the newer package. The server dumps the database before it applies a new migration.

## Decisions already made

These were settled on 2026-09-26. Build on them, and don't reopen them without asking the owner.

1. **Production uses the operating system's PostgreSQL, not Docker.** That's `postgresql` from apt on Linux and WSL, and `postgresql@17` from Homebrew on macOS. Docker stays for development (the Supabase CLI) and for the optional closet camera (Frigate). Why: a package manager can install Postgres but not Docker. A systemd unit that depends on `postgresql.service` is the normal Linux shape. On macOS it removes Docker Desktop, which only runs while someone is logged in.
2. **The Stockroom database role is a superuser** in a local cluster that holds nothing else. The restore runs `set local session_replication_role = replica` (`internal/stockroom/restore.go`), which needs one. Postgres listens on loopback only, and setup checks that.
3. **On Linux the service runs as the teacher's own account by default**, the user who ran `sudo`. The admin panel's folder picker (`internal/stockroom/local_folders.go`) starts at the service user's home and offers Documents and external drives (`/media/<user>`) as shortcuts. On a closet PC with a desktop, that's the useful answer. A dedicated system user is an option (`--service-user`) for headless servers.
4. **The `.deb` ships from GitHub releases first.** A signed apt repository comes later (section 7).
5. **`scripts/install.sh`, `deploy/docker-compose.yml`, `deploy/stockroom-run.sh` and the two service templates in `deploy/` get deleted** once sections 5 and 6 pass. That's when the package path covers Linux and macOS. `deploy/camera/` stays.

## How to work on this file (for agents)

- Read `CLAUDE.md` first (§3, §5, §7, §8, §9 and §11 matter most here), `docs/decisions.md` for the reasoning in the area you touch, then `CONTEXT.md`, `docs/agents/*.md` and `docs/adr/`.
- Never commit code to `main`. Make a branch named for the task and open a pull request into `main`; the owner merges it. A change that touches only `.md` files may go straight to `main`.
- Follow `.claude/skills/unslop/SKILL.md` in everything you write: docs, comments, commit messages, PR descriptions.
- `./scripts/dev.sh test` must pass before a commit. The pre-commit hook runs it.
- Each task below says what to change, where, and how you know it's done. When a task needs something that doesn't exist yet (hardware, a school decision, a real Windows machine), it says so. Do the parts you can and leave the rest ticked open with a note.

# Track A: parallel work

Nothing in track A blocks track B, and nothing in track B blocks track A. Track A can run alongside any main-track section, by a different agent if you like.

## A1. Closet camera: what's left

Built for macOS and Linux and proven with Frigate against sample footage. See [`docs/design/closet-camera.md`](docs/design/closet-camera.md) and `docs/decisions.md` (2026-09-26). It ships off: nothing records until an admin turns it on in Admin → Settings → Closet camera.

- [ ] **Test the Linux webcam path with any USB webcam.** No QuickCam needed. On a Linux machine with Docker, run `STOCKROOM_HOME=$(mktemp -d) deploy/camera/camera.sh up webcam`. That maps `/dev/video0` into the container through `deploy/camera/compose.linux-webcam.yml`. Turn the camera on in Admin → Settings, walk in front of it, and confirm one walked-in row, one walked-out row, a snapshot and a playable clip in Admin → Activity. Record CPU and RAM with the room empty and with a person in view, and add them to `docs/design/closet-camera.md` §5. On an Intel machine `camera.sh` picks the OpenVINO detector. Record which detector ran.
- [ ] **Test the live webcam on macOS through go2rtc.** Someone has to click the macOS camera-permission prompt once. Until they do, AVFoundation hangs without an error. Then repeat the check above.
- [ ] **Get the school's written approval for recording students, and put a sign on the closet door.** Defaults agreed with the owner: off until an admin turns it on, recordings kept 30 days unless marked keep, the activity log kept forever and readable by admins only. Record the approval (who, when) in `docs/decisions.md`. Blocked on the school.
- [ ] **Measure with the Logitech QuickCam Pro 9000** once it arrives. It outputs MJPEG up to 960×720 and has no built-in H.264. Capture MJPEG at 960×720, record at 10 to 15 fps, and detect on a 640×360 downscale at about 5 fps. Update the size and frame rates in `camera.sh` if the measurements disagree.
- [ ] **Tune on the closet PC**, once it exists. Check that the OpenVINO detector is in use on Intel and that Quick Sync hardware encoding is on where available. Measure CPU over a full school day. Mount the camera over the door facing into the closet. The code assumes that placement, so it uses no doorway zone.
- [ ] **Build the motion-only fallback only if the closet PC can't run person detection.** The fallback empties Frigate's `objects.track`, sets `review.alerts` to motion, and changes the connector in `internal/stockroom/camera_detector.go` to read motion review items instead of person events, logged as "activity in the closet". Decide this on the closet PC.

Hardware reference for the closet PC with the camera on:

| Component | Minimum | Recommended |
|---|---|---|
| CPU | 64-bit x86 with AVX and AVX2 (Frigate needs both). Intel preferred, for OpenVINO | Intel N100 or newer, or a 6th-generation Core i3 or better |
| RAM | 8 GB | 16 GB |
| Storage | SSD with 50 GB free | SSD with 100 GB or more free |
| Camera | Any UVC USB webcam | Logitech QuickCam Pro 9000 |
| Accelerator | None | Google Coral USB, if the CPU can't keep up |

The camera isn't supported under WSL, which would need `usbipd-win` to pass the webcam through. It's also not supported on native Windows.

## A2. Sign-in photo wall as a film strip

Today the wall hands out single-use tiles from a disk cache. It keeps 48 ready and gives out 32 per sign-in page load, deleting each tile 15 minutes after it was shown. The code:

| File | Role |
|---|---|
| `internal/stockroom/photowall.go` | The reel: buffering, serving, and deleting after use |
| `photowall_drive.go` | Listing the folder and fetching a photo with `rclone cat` |
| `photowall_image.go` | EXIF, aspect-ratio gate, crop, resize to 900×600 JPEG |
| `photowall_admin.go` | The admin screen |
| `photowall_google.go` | Starting the wall after a Google sign-in |
| `packages/ui/src/lib/components/app/photo-wall.svelte` | The sign-in screen's columns |
| [`docs/design/signin-photo-wall.html`](docs/design/signin-photo-wall.html) | The design |

The new design keeps a set of about 150 photos in memory and scrolls them as film strips.

- [ ] **Replace the single-use reel with a photo set.** The set holds N photos (default 150, stored in `app_settings`, capped at a maximum you choose and write down, for example 400). Prepare the set once and cycle through it continuously.
- [ ] **Pick at random across folders, with a per-folder cap.** Choose a folder first and then a photo inside it, or cap any one folder at a small share of the set. Either way, a folder of 200 photos from one event can't fill the wall.
- [ ] **Keep the photos in memory only.** 150 tiles at about 100 KB each is about 15 MB of RAM. Serve tiles from memory at `/signin-photos/{id}.jpg` and never write them to disk. Rebuild the set after a restart. Fall back to a disk cache only if measurements show the rebuild costs too much time or bandwidth.
- [ ] **Refresh gradually.** On a slow timer, swap one photo for a new random pick, so the strip changes over the day without downloading everything again.
- [ ] **Drop photos that disappear from Drive** at the next listing.
- [ ] **Fill in the background.** The strip shows whatever is ready and grows as more arrives.
- [ ] **Keep the existing guards.** One download at a time with a delay between them. The same rclone remote as the backup. Sign-in must not change at all if Drive is unreachable. Never send the folder id or a Drive URL in any response (`docs/decisions.md`, 2026-09-18).
- [ ] **Fill faster.** Today it takes one photo every 20 to 25 seconds, so about an hour for 150, repeated after every restart.
  - Download by file id rather than by path, so rclone doesn't resolve every folder on each fetch. Try `rclone backend copyid` and `--drive-root-folder-id` set to the file's parent, and measure both.
  - Check whether `rclone lsjson --metadata` returns Drive's image width and height. If it does, skip portrait and panoramic photos without downloading them.
  - Measure fill time and total download size against the real folder.
- [ ] **Film-strip columns on the sign-in screen.** Photo frames with sprocket-hole edges, scrolling at a steady, slow pace, cycling through the whole set. Load images lazily as they approach the visible area. Append new photos to the end of the strip with no visible jump.
  - Replace the batch endpoint `GET /signin/photos` with one that returns the current set. It stays unauthenticated and returns only tile URLs.
  - When the set is empty or the feature is off, draw no strips and leave the sign-in field alone.
  - Respect `prefers-reduced-motion`.
- [ ] **Admin → Photo wall.** Add a set-size setting, a fill-progress line ("112 of 150 ready") and a **Reshuffle now** button.
- [ ] **Remove the old settings.** Delete `SIGNIN_PHOTOS_BATCH`, `SIGNIN_PHOTOS_TTL_MINUTES`, `SIGNIN_PHOTOS_DIR` and `SIGNIN_PHOTOS_COUNT` from `internal/stockroom/config.go` and `.env.example`, and remove the `.cache/signin-photos` directory handling.
- [ ] **Tests.**
  - Go tests for folder-balanced selection, the per-folder cap, gradual refresh, removing deleted photos, switching folders, and the memory cap. Use the fake rclone the existing photo-wall tests use.
  - Update the frontend smoke tests for the new endpoint.
  - By hand, unplug the network and confirm sign-in is unaffected, then watch a full cycle on the slowest machine available.
- [ ] **Docs.** Update `docs/design/signin-photo-wall.html`, `docs/api.md` (the endpoint rows) and `docs/decisions.md`.
- [ ] **On the closet PC**, press **Sign in with Google** in Admin → Photo wall, then **Choose from Google Drive** and pick the student media photos folder. Blocked on section 8.

## A3. Repository

- [ ] **Branch protection on `main`**, requiring `tests` and `tests-windows`, plus any job section 1 adds. [`CI.md`](CI.md) has the exact `gh api` command. The owner has to run it, or an agent with admin rights on the repository.
- [ ] **Set the repository description and topics.** Topics: `school`, `inventory`, `checkout`, `barcode`, `education`, `equipment`, `go`, `svelte`, `self-hosted`. Use `gh repo edit --description ... --add-topic ...`.
- [ ] **Add screenshots or a short recording to the README**, with the example data loaded (setup wizard → Load examples). Show sign-in, browse, the cart, and Admin → Activity. Put the images in `docs/images/`.
- [ ] **Make `./scripts/dev.sh test` repeatable.** A second run without `supabase db reset` fails `supabase/tests/080_seed.test.sql`, because the Go suite changes seed rows. Either make 080 assert only rows the Go suite never touches, or make the Go tests that change seed rows put them back. Done when `./scripts/dev.sh test` passes twice in a row.

## A4. Product work for later

None of this is needed for v1.0. Pick any of it up when track B is waiting on hardware or people.

- [ ] **Settings for the remaining hardcoded rules.** The maximum checkout length (7 days), whether being overdue blocks checkout, the session idle timeout (`SESSION_IDLE_MINUTES` today), and the scanner threshold (`SCAN_KEY_THRESHOLD_MS` in `packages/ui/src/lib/scanner.ts`). Store them in `app_settings` like `due_time`, expose them through `GET /signin/config` where the sign-in screen needs them, and add them to Admin → Settings.
- [ ] **School holidays.** A loan is due on the next weekday after its last day of use (`internal/stockroom/due.go`), so a loan ending before a holiday falls due on the holiday. Add a list of closed dates, maintained by an admin, that `due.go` and `packages/ui/src/lib/due.ts` both skip.
- [ ] **`docs/HARDWARE.md`.** A scanner buying guide (plain USB HID keyboard-wedge, sends Enter after each code) and the physical setup: placement, counter height, sleep settings, kiosk browser.
- [ ] **`docs/UPGRADING.md`**, once there are two released versions.
- [ ] **`docs/FAQ.md`.**
- [ ] **A support bundle.** A `stockroom support-bundle` subcommand that writes `doctor` output (section 4) and the recent log with secrets removed into one zip.
- [ ] **Asset photos from a webcam, and by drag and drop.**
- [ ] **Assign student numbers from a range** for groups that have no ID numbers.
- [ ] **Add users by pasting a list of names**, without a CSV.
- [ ] **A data-deletion flow for a student**, beyond archiving or deleting the account. The activity log is append-only by design (`docs/adr/0003`), so this needs a decision first.

# Track B: the main path to production

Do these in order. Each section's dependencies are listed at its start.

## 1. A self-contained binary

Needs nothing. Everything after this depends on it.

Today the server (`server/main.go`, built as `stockroom`) has several gaps:

- It finds its `.env` by walking up from its working directory (`findDotEnv` in `internal/stockroom/config.go`).
- It defaults `UPLOADS_DIR` to `./uploads` and the photo wall's cache to `./.cache/signin-photos`, both relative.
- It relies on `scripts/install.sh` to take a database dump before an upgrade.
- It runs `rclone` by bare name (`rcloneBinary` in `internal/stockroom/target_drive.go`), which a service manager's minimal `PATH` may not find.
- `cmd/restore` is a second binary.

Tasks:

- [ ] **Add subcommands** to the one binary, dispatched in `server/main.go` on `os.Args[1]`:
  - `serve` is today's behaviour, and running with no arguments still means `serve`, so `go run ./server` and `dev.sh` keep working.
  - `setup` (section 2), `service install|uninstall|start|stop|status` (section 2), and `doctor` (section 4).
  - `restore` does what `cmd/restore` does, calling the same `RestoreFromZip` with `stockroom.LocalCLIActor()`. Move the CLI's body into a shared function and keep `cmd/restore/main.go` as a thin wrapper until the docs stop mentioning `go run ./cmd/restore`.
  - `version` prints the version, the commit and the schema version.
  - `open` opens the browser at the configured address, using `xdg-open` on Linux, `open` on macOS, and `wslview` or `cmd.exe /c start` under WSL.

  Use the standard `flag` package with one `FlagSet` per subcommand, no new dependency. Every subcommand prints which config file it used.
- [ ] **Read the config from a known path.** Add `stockroom.LoadConfigFrom(path string)` and a `--config` flag on every subcommand. The first match wins:
  1. `--config`
  2. `STOCKROOM_CONFIG`
  3. `findDotEnv` from the working directory, so development is unchanged
  4. `/etc/stockroom/stockroom.env` on Linux
  5. `$(brew --prefix)/var/stockroom/stockroom.env` on macOS

  `Config.EnvPath` must still point at the file actually read, because the setup wizard's failsafe step writes to it (`setEnvValues` in `envfile.go`). Add tests to `config_test.go` for the precedence.
- [ ] **Refuse relative data paths in production.** When the config came from `--config` or a system path, a relative `UPLOADS_DIR` or photo-wall directory is a startup error that names the variable. The same rule already applies to backup folders (`validateDir`, `docs/decisions.md`, 2026-09-21). Setup always writes absolute paths: `/var/lib/stockroom/uploads`, `/var/lib/stockroom/backups`, `/var/lib/stockroom/photo-backups`, `/var/lib/stockroom/cache`.
- [ ] **Stamp the version.** Add `var version = "dev"` and `var commit = ""` in `server/main.go`, set by `-ldflags "-X main.version=… -X main.commit=…"`.
  - Add `version` to the `GET /health` response.
  - Show it in the admin panel's sidebar footer, read from `/health`.
  - Put it in the backup manifest next to the schema version (`internal/stockroom/archive.go`).
- [ ] **Dump the database before migrating.**
  - Add `stockroom.PendingMigrations(ctx, pool, fsys)`, returning the versions `Migrate` would apply.
  - In `serve`, before `Migrate`: if anything is pending and the database already has a `profiles` table, run `pg_dump` into `<backups dir>/pre-migrate/pre-migrate-<last applied>-to-<newest>-<yyyymmdd-hhmmss>.sql`. Create the file with mode 600; it's the whole roster.
  - Pass the password in the `PGPASSWORD` environment variable, never in arguments, which every local user can read through `ps`. Parse `DATABASE_URL` into host, port, user and database flags.
  - Find `pg_dump` from `PG_DUMP` in the config, then `PATH`, then `/usr/lib/postgresql/*/bin` (highest version first), then `$(brew --prefix)/opt/postgresql@17/bin`, which isn't on `PATH` because that formula is keg-only.
  - A new config key, `PRE_MIGRATE_DUMP`, controls this. `required` refuses to migrate, and exits with a message naming the fix, when the dump fails or `pg_dump` is missing. `off` skips it. Setup writes `required`. When the key is unset it means `off`, so development machines with no `pg_dump` keep working.
  - Keep the newest 10 dumps and delete older ones.
  - Test it with a fake `pg_dump` script on `PATH`: one that succeeds, and one that fails and must stop the migration.
- [ ] **Refuse a database newer than the binary.** If the database records a migration version the embedded set doesn't contain, `serve` must exit and say the database is newer than this version of Stockroom and to install the newer version. Check `Migrate` in `internal/stockroom/migrate.go` and add this if it's missing, with a test.
- [ ] **Find `rclone` without relying on `PATH`.**
  - Turn the `rcloneBinary` constant into a variable resolved once at startup. The order is `RCLONE_BINARY` from the config, `exec.LookPath("rclone")`, then `/usr/bin/rclone`, `/usr/local/bin/rclone`, `/opt/homebrew/bin/rclone`.
  - Record the result and its `rclone version` output.
  - The backup screen and `doctor` show the path and version.
  - Keep the fake rclone the tests put on `PATH` working.
- [ ] **Decide the minimum rclone version.** Debian 12 and Ubuntu 24.04 package rclone 1.60. Stockroom was built and verified against 1.75. Test 1.60 against the fake Google flow and against a real account:
  - `rclone authorize drive` and both output shapes `authorizeToken` reads (`drive_authorize.go`)
  - `rclone config create … token=…` with the school's client id (`google.go`)
  - `RCLONE_DRIVE_CLIENT_ID` and `RCLONE_DRIVE_CLIENT_SECRET` in the environment
  - `lsjson --no-modtime --max-depth`, `cat` and `copy`

  If 1.60 works, the `.deb` depends on the distro's `rclone`. If it doesn't, the `.deb` uses `Recommends: rclone` rather than `Depends`, and `get.sh` installs rclone from rclone.org's own `.deb`. Either way, add a `minRcloneVersion` constant that `doctor` and the backup screen check. Record the result in `docs/decisions.md`.
- [ ] **Find the oldest PostgreSQL that works.** Debian 12 ships 15, Ubuntu 24.04 ships 16, Ubuntu 22.04 ships 14. Add a `tests-postgres` job to `.github/workflows/tests.yml` with a matrix of `postgres:14`, `postgres:15`, `postgres:16` and `postgres:17` service containers. Model it on the `tests-windows` job: build the binary, start it against the empty database so it applies the migrations itself, wait for `/health`, load `supabase/seed.sql` with `psql`, then run `STOCKROOM_REQUIRE_DB=1 go test ./... -count=1 -p 1`. The lowest version that passes becomes `N` in section 2's `Depends: postgresql (>= N)`. Record it in `CLAUDE.md` §5. If 14 fails, Ubuntu 22.04 is unsupported unless it adds the PGDG repository, and `get.sh` should say so.

**Done when:**

- `stockroom serve --config /tmp/x.env` runs from any working directory.
- `stockroom version` prints the version.
- An upgrade with a pending migration writes a dump first.
- The Postgres matrix is green.
- `dev.sh up` and `dev.sh test` behave exactly as before.

## 2. The Linux package and `stockroom setup`

Depends on section 1.

- [ ] **Build a `.deb` with nfpm**, configured in `.goreleaser.yaml` (section 3), for amd64 and arm64. Name the files without a version (`stockroom_amd64.deb`, `stockroom_arm64.deb`) so `https://github.com/Kathir-D/Stockroom/releases/latest/download/stockroom_amd64.deb` always points at the newest one. The package contains:
  - `/usr/bin/stockroom`
  - `/lib/systemd/system/stockroom.service`
  - `/usr/share/stockroom/camera/`, a copy of `deploy/camera/`, so `setup --with-camera` works without a checkout
  - `/usr/share/doc/stockroom/`, with the README and `docs/INSTALL.md`
  - `Depends: postgresql (>= N), postgresql-client, ca-certificates` plus rclone as section 1 decided. Also `Recommends: xdg-utils`.

  Keep the packaging files in a new `packaging/linux/` directory.
- [ ] **The systemd unit** (`packaging/linux/stockroom.service`):
  - `After=network.target postgresql.service` and `Wants=postgresql.service`
  - `ExecStart=/usr/bin/stockroom serve --config /etc/stockroom/stockroom.env`
  - `Restart=on-failure`, `RestartSec=2`
  - `ProtectSystem=full` with `ReadWritePaths=/etc/stockroom`, plus `PrivateTmp=yes` and `NoNewPrivileges=yes`
  - Leave out `ProtectSystem=strict` and `ProtectHome`. An admin can pick any backup folder in the panel, the teacher's Documents and USB drives under `/media` included, and those two options would make the backup fail on exactly the folders the picker offers.
  - No `User=` in the unit itself; setup adds it as a drop-in.
- [ ] **Maintainer scripts.**
  - `postinst` enables nothing on a first install. A unit that crash-loops on a missing config would be the first thing a new user saw. On an upgrade it runs `systemctl daemon-reload` and `systemctl try-restart stockroom`.
  - `prerm` stops the service on removal.
  - `postrm purge` removes `/etc/stockroom` only.
  - Nothing in the package ever drops the database or deletes `/var/lib/stockroom`.
- [ ] **`stockroom setup`** must run as root and be safe to run again: a second run repairs rather than reinstalls. In order:
  1. **Detect the platform.** Refuse anything that isn't Linux or macOS. Detect WSL from `WSL_DISTRO_NAME` or `/proc/sys/fs/binfmt_misc/WSLInterop`, and if systemd isn't PID 1, print the fix: `[boot] systemd=true` in `/etc/wsl.conf`, then `wsl --shutdown` from Windows.
  2. **PostgreSQL.** Start and enable `postgresql.service` if it isn't running. As the `postgres` user over the local socket (peer authentication, how Debian and Ubuntu ship it), read `SHOW listen_addresses` and `SHOW port`. Refuse to continue if `listen_addresses` includes anything other than `localhost`, `127.0.0.1`, `::1` or empty, and print the line to change in `postgresql.conf`.
  3. **Role and database.** Create role `stockroom` (`superuser`, `login`) with a generated 32-character alphanumeric password from `crypto/rand`, and database `stockroom` owned by it. If they already exist and a config file exists, leave both alone. If the role exists but there's no config, reset the password and write a new config.
  4. **Config.** Write `/etc/stockroom/stockroom.env`, mode 600, owned by the service user, in directory `/etc/stockroom` at mode 700 with the same owner. It holds:
     - `DATABASE_URL=postgresql://stockroom:<pw>@127.0.0.1:<port>/stockroom`
     - `SERVER_ADDR=127.0.0.1:8080`
     - the absolute data paths from section 1
     - `PRE_MIGRATE_DUMP=required`
     - `SESSION_IDLE_MINUTES=10`
     - the failsafe values
     - `BACKUP_DIR` and `PHOTO_BACKUP_DIR` as first-boot seeds. Under WSL, `BACKUP_DIR` defaults to the Windows Documents folder (section 11).

     Single-quote values the way `install.sh` does. Never overwrite an existing config.
  5. **Failsafe admin.** Ask for the number and password with echo off. Take `--admin-number` and `--admin-password-file` (`-` for stdin) for unattended use, and never accept a password as an argument. Apply the same checks `install.sh` does: both or neither, digits only, 8 to 72 characters, no single quote.
  6. **Service user.** Default to `$SUDO_USER`, or take `--service-user`. With `--service-user stockroom`, create a system user with home `/var/lib/stockroom`. Write `/etc/systemd/system/stockroom.service.d/user.conf` with `User=` and `Group=`. Create `/var/lib/stockroom/{uploads,backups,photo-backups,cache}` owned by that user. rclone keeps its Google token in the service user's `~/.config/rclone/rclone.conf`, so switching service users later means signing in to Google again. Setup should print that when it changes the user.
  7. **Start.** `systemctl daemon-reload`, `systemctl enable --now stockroom`. Poll `/health` for up to two minutes. On failure, print `journalctl -u stockroom -n 40`.
  8. **Finish.** Print the address and the next steps: sign in, the web wizard, and the reboot test from section 5. Open the browser with `xdg-open` as `$SUDO_USER` when there's a desktop session.

  Flags: `--non-interactive` (fails instead of prompting), `--no-service`, `--no-open`, `--service-user`, `--admin-number`, `--admin-password-file`, `--addr`, `--with-camera`.

  **Prompts must read from `/dev/tty`, not stdin.** Under `curl … | sudo bash`, stdin is the script itself.

  Put the Linux logic in a new `internal/setup` package (or `server/setup_*.go`) behind small interfaces for "run a command" and "run SQL as postgres", so it can be unit-tested without root. Integration coverage comes from section 5's CI job.
- [ ] **`setup --with-camera`** checks that Docker is installed and running, and says how to install it if not. Setup never installs Docker itself. Then it runs `/usr/share/stockroom/camera/camera.sh up webcam` with the recordings directory at `/var/lib/stockroom/recordings`.
- [ ] **`stockroom service install|uninstall|start|stop|status`** wraps the `systemctl` calls above. Setup uses the same code. On macOS it manages LaunchDaemons (section 6).
- [ ] **`scripts/get.sh`.**
  - Re-exec itself with `sudo` if not root.
  - Read `/etc/os-release`. Support `debian` and `ubuntu`, including under WSL, and print manual instructions for anything else.
  - Pick `amd64` or `arm64` from `dpkg --print-architecture`.
  - Download the `.deb` and `checksums.txt` from the latest release, or from `STOCKROOM_VERSION` when it's set. Verify the checksum and stop on a mismatch.
  - `apt-get update`, `apt-get install -y ./stockroom_<arch>.deb`, then `stockroom setup`, passing through any extra arguments.

  Keep it short, commented, readable top to bottom, and `set -euo pipefail`. A teacher is being asked to pipe it into a shell. Running it again is the upgrade.

**Done when:** on a fresh Ubuntu 24.04 VM, `curl … | sudo bash` ends with Stockroom answering at `http://127.0.0.1:8080`, the setup wizard showing, and `systemctl is-enabled stockroom` saying `enabled`.

## 3. Releases

Depends on sections 1 and 2.

`.github/workflows/release.yml` builds four binaries on a `v*` tag and has never been used.

- [ ] **Replace it with GoReleaser.** Add `.goreleaser.yaml`:
  - `before.hooks`: `npm ci`, `npm run build --workspace=web-app`, `test -f web-app/dist/index.html`. The binary embeds `web-app/dist`, so building Go first ships an empty UI.
  - `builds`: `./server`, binary `stockroom`, `CGO_ENABLED=0`, `goos` linux and darwin, `goarch` amd64 and arm64, `-trimpath`, `-ldflags "-s -w -X main.version={{.Version}} -X main.commit={{.ShortCommit}}"`. Drop the Windows build.
  - `archives` as `tar.gz`, and `checksum` with `name_template: checksums.txt`.
  - `nfpms` from section 2.
  - The Homebrew formula from section 6.
- [ ] **Rewrite `release.yml` to run GoReleaser.** Keep three things from the current workflow:
  - `permissions: contents: read` by default, with write only in the publish step
  - no Go cache in a release build
  - `workflow_dispatch` running `goreleaser release --snapshot --clean`, which builds without publishing

  The tap needs a fine-grained token with contents write on `Kathir-D/homebrew-stockroom` only, stored as `HOMEBREW_TAP_TOKEN`. The owner has to create it.
- [ ] **Release a candidate.** Tag `v0.9.0-rc.1` and install it with `get.sh` (use `STOCKROOM_VERSION=v0.9.0-rc.1`) on a machine that isn't the development Mac.

**Done when:** a pushed tag produces a GitHub release with the archives, both `.deb` files, `checksums.txt`, and a formula commit in the tap.

## 4. Upgrades, `doctor`, uninstall, docs

Depends on section 1. Can run alongside sections 2 and 3.

- [ ] **Test the upgrade path.** Install version A, add data, install version B carrying a new migration. Confirm the pre-migrate dump exists, the migration applied, and the data survived. Section 5's CI job automates this.
- [ ] **`stockroom doctor`.** Each check prints OK, WARN or FAIL with one line saying how to fix it, and the command exits non-zero on any FAIL. Checks:
  - which config was read, and that its data paths are absolute
  - Postgres reachable, listening on loopback only, and its version
  - migrations current, and whether the database is newer than the binary
  - service installed, enabled and running, and running as which user
  - who holds the server's port, if it isn't Stockroom
  - `rclone` found, and its version against `minRcloneVersion`
  - `pg_dump` found when `PRE_MIGRATE_DUMP=required`
  - free disk space on the data, backup and recordings folders
  - age of the last successful backup, read the way `BackupStatus` reads it
  - under WSL, that systemd is on, the Windows startup task exists (section 11), and the clock is within a minute of Windows's (`powershell.exe -c Get-Date`)
- [ ] **Postgres major-version upgrades.** Debian and Ubuntu keep a cluster on its version until someone runs `pg_upgradecluster`, and `postgresql@17` is pinned on macOS, so nothing changes by surprise. Document the manual path in `docs/INSTALL.md`: Admin → Backup → Export everything, upgrade Postgres, `stockroom setup`, then restore the export in Admin → Backup.
- [ ] **Uninstall.** Document `sudo apt remove stockroom`, which keeps data and config, and `sudo apt purge stockroom`, which also removes the config. Also document the commands that delete everything else, for anyone who really wants it gone: `sudo -u postgres dropdb stockroom`, `sudo -u postgres dropuser stockroom`, `sudo rm -rf /var/lib/stockroom`.
- [ ] **Rewrite `docs/INSTALL.md`** in three sections, Linux, macOS, and Windows (WSL), each starting from the one command. Put `install.sh` in a short "installing from a checkout" section until decision 5 deletes it.
- [ ] **Update the README** Installation and Requirements sections to match. Update `CLAUDE.md` §3, §5, §8 (the new files and `packaging/`) and §9, and add an entry to `docs/decisions.md`.

## 5. Prove it on Linux

Depends on sections 2, 3 and 4. Linux is ready for the closet PC once this passes.

- [ ] **A CI job, `package-linux`,** on `ubuntu-24.04`. GitHub's runner is a full VM with systemd, so no container is needed.
  1. Build the `.deb` with `goreleaser release --snapshot --clean`.
  2. `sudo apt-get install -y ./dist/stockroom_amd64.deb`.
  3. `sudo stockroom setup --non-interactive --no-open --admin-number 900100 --admin-password-file <(echo password123)`.
  4. Check `/health`, then `systemctl restart stockroom` and check again.
  5. Build a second `.deb` from a commit that adds a dummy migration in the test only, install it over the first, and confirm a file appeared in `pre-migrate/`.
  6. `stockroom doctor` exits 0.

  Add a `debian:12` container variant that runs `setup --no-service` and starts `stockroom serve` by hand, since the container has no systemd. Add both jobs to `CI.md` and the path filter.
- [ ] **Clean-VM run by hand.** Fresh Ubuntu 24.04 desktop VM. Run the one-line install and time it. In the web wizard, load nothing, import `examples/` files through the real screens, and print labels to PDF. Connect Google in Admin → Settings and choose a Drive backup folder. Do one checkout and one return with a keyboard standing in for the scanner. Write down every place the docs fell short and fix them.
- [ ] **Reboot test.** Reboot and don't log in. From another machine or a console, confirm that Postgres and Stockroom are running and `/health` answers. Log in and confirm the nightly backup's boot catch-up ran: it logs "running now" when the last success is stale. At the end of its output, setup offers this check as a printed step.
- [ ] **Power-cut test.** Hard-power-off the VM mid-use. It comes back with no data lost that was committed before the cut.

**Done when:** all four pass and the fixes are merged.

## 6. macOS

Depends on section 3.

- [ ] **Create the Homebrew tap repository** `Kathir-D/homebrew-stockroom`; the owner has to create it. GoReleaser writes the formula into it. The formula needs:
  - the release binary for the Mac's architecture
  - `depends_on "postgresql@17"` and `depends_on "rclone"`
  - a `test do` block running `stockroom version`
  - caveats telling the user to run `stockroom setup`

  GoReleaser v2.10 and later deprecate `brews` in favour of `homebrew_casks`. Use whichever the current GoReleaser supports and make sure the result still installs both dependencies. If it produces a cask, add a post-install step that removes the quarantine attribute from the binary, because Homebrew quarantines cask downloads and the binary isn't signed.
- [ ] **`stockroom setup` on macOS.**
  - Data goes in `$(brew --prefix)/var/stockroom` and config in `$(brew --prefix)/var/stockroom/stockroom.env`.
  - Postgres runs as the installing user (Homebrew's default), with `trust` or peer auth for that user locally. Create the `stockroom` role and database the same way as on Linux.
  - Both Postgres and Stockroom must start at boot without a login. `brew services` can't do that: it makes a LaunchAgent, which needs a login, and under `sudo` it runs Postgres as root, which Postgres refuses. Write LaunchDaemons to `/Library/LaunchDaemons/` with `UserName` set to the installing user, one for `postgresql@17` (`com.stockroom.postgresql.plist`) and one for Stockroom (`com.stockroom.server.plist`). Use `RunAtLoad` and `KeepAlive`, and log to `$(brew --prefix)/var/log/stockroom.log`. Setup needs `sudo` for this step only.
  - If Homebrew's own `homebrew.mxcl.postgresql@17` agent is loaded, unload it, so two Postgres processes don't fight over the data directory.
- [ ] **Prove it on a real Mac.** Install from the tap, run setup, reboot without logging in, and confirm `/health` answers from another machine through SSH port forwarding, or check the daemon logs after logging in. If boot-time start can't be made to work, fall back to today's documented answer, automatic login (System Settings → Users & Groups), and record why in `docs/decisions.md`.
- [ ] **Delete the old installer** (decision 5): `scripts/install.sh`, `deploy/docker-compose.yml`, `deploy/stockroom-run.sh`, `deploy/com.stockroom.server.plist.template` and `deploy/stockroom.service.template`. Remove every mention from the docs, and the Docker requirement from the README. Keep `deploy/camera/`. On the development Mac, move the existing Docker-based install over with Admin → Backup → Export everything, then restore it on the new install.

## 7. Signed apt repository

Depends on section 5.

- [ ] **Publish the `.deb` to an apt repository** so `apt upgrade` picks up new versions with the rest of the system. Either use Cloudsmith's free open-source tier, or build a GPG-signed repository on GitHub Pages from the release workflow. With the Pages option, `reprepro` or `aptly` generates the repository and the signing key is a repository secret the owner creates.
- [ ] **Switch `get.sh`** to add the repository's key and source list, then `apt-get install stockroom`, instead of downloading a file.
- [ ] **Document the key's fingerprint** in `docs/INSTALL.md`.

## 8. The closet PC and its hardware

Depends on section 5. Blocked on the school providing the PC and the scanner.

- [ ] **Confirm with the school that the closet PC can run Linux.** If it must stay on Windows, it uses WSL (section 11) and has no camera.
- [ ] **Check the PC against the hardware table in A1**: CPU model and AVX2 (`grep avx2 /proc/cpuinfo`), RAM, SSD, free space.
- [ ] **Install Ubuntu 24.04 LTS** and set:
  - never sleep or suspend
  - automatic login for a local kiosk account. The server doesn't need a login, but the screen students use does.
  - time zone set to the school's. Due times (`app_settings.due_time`, 15:30) and the nightly backup hour use the server's local clock.
  - NTP on
- [ ] **Kiosk browser.** At login, open `http://127.0.0.1:8080` full-screen, for example an autostart `.desktop` entry running `chromium --kiosk --app=http://127.0.0.1:8080`. Document it in `docs/HARDWARE.md` (A4) or `docs/INSTALL.md`.
- [ ] **Buy a plain USB HID keyboard-wedge barcode scanner** that sends Enter after each code. Tune `SCAN_KEY_THRESHOLD_MS` (50 ms today, in `packages/ui/src/lib/scanner.ts`) with the Ctrl+Shift+D diagnostic, and record the model and value in `CLAUDE.md` §10.
- [ ] **Choose the photo-mirror disk.** Decide whether it gets a second physical disk. The mirror is the only copy of item photos besides `uploads/` (`docs/design/backup.md` §H).
- [ ] **Enter the real inventory.** Import categories, then assets (Admin → Assets → Import CSV or Add several). Print labels at 100% and stick them on. Import the roster.

## 9. Backups before go-live

Depends on section 8.

- [ ] **Create the school's own Google OAuth client** (`docs/BACKUP-SETUP.md` step 3c). Set it to **Published**, not Testing: a Testing client's refresh tokens expire after seven days, which kills the backup a week after setup. Paste it into Admin → Settings → Google account → Advanced and sign in again. rclone's shared client stops working during 2026, and the Drive backup with it.
- [ ] **Connect Google Drive on the closet PC**, choose the backup folder, and confirm the next nightly run reaches Drive (Admin → Backup shows the target's last success).
- [ ] **Restore from that Drive backup once**, on another machine, through Admin → Backup → Restore, before relying on it.
- [ ] **Configure everything on a fresh install without opening a text editor.** Any step that needs one is a bug to fix.
- [ ] **Optional: test the GitHub target** against a real private repository and restore from it once. It's labelled experimental, and Drive alone is enough for v1.

## 10. People, then v1.0

Depends on section 9.

- [ ] **Usability test** with someone who has never seen Stockroom. Measure the time to their first checkout and count how often they needed help. Fix what they tripped on.
- [ ] **Pilot** in one class for a term. Collect problems as GitHub issues (`docs/agents/issue-tracker.md`).
- [ ] **Tag `v1.0.0`** once the closet PC has passed section 5's reboot test and the pilot has run for at least two weeks without a data problem.

## 11. Windows through WSL

Depends on section 5. Last on the main path.

Stockroom runs inside WSL 2's Ubuntu 24.04 exactly as on Linux: the same `.deb`, the same `setup`, the same systemd unit. The browser, the barcode scanner and the label printer stay on the Windows side. The scanner types into the Windows browser like a keyboard and needs nothing. The work here is making WSL behave like a server that's always on.

- [ ] **`scripts/get.ps1`**, run from an administrator PowerShell. It must say so and stop if not elevated. It:
  1. runs `wsl --install -d Ubuntu-24.04 --no-launch` if that distribution is missing, then tells the user to reboot and run it again if Windows asks
  2. makes sure `/etc/wsl.conf` has `[boot] systemd=true`, and runs `wsl --shutdown` if it had to add it
  3. runs `get.sh` inside the distribution as root, passing through the failsafe flags
  4. registers the startup task below
- [ ] **Start WSL at boot and keep it running.** WSL only runs while something holds it open, and it shuts down when idle. Register a Task Scheduler task named `Stockroom WSL`, triggered at system startup and run whether or not a user is logged on, that runs `wsl.exe -d Ubuntu-24.04 --exec /usr/bin/sleep infinity`. That boots the distribution, systemd starts Postgres and Stockroom, and the sleeping process keeps it from idling out. `doctor` checks for the task.
- [ ] **Check the Windows browser reaches the server** at `http://localhost:8080` through WSL's localhost forwarding. Test with the default networking and with `networkingMode=mirrored` in `.wslconfig`. The server's loopback `Host` check (`withHostCheck` in `server/router.go`) already accepts `localhost`.
- [ ] **Test Google sign-in early.** `rclone authorize` listens on `127.0.0.1:53682` inside WSL, and Google redirects the Windows browser there. If forwarding doesn't carry it, the paste-a-code fallback in the sign-in dialog ("Signing in on a different computer?") has to work, and the docs have to point at it.
- [ ] **Put backups on the Windows side.** Under WSL, setup seeds `BACKUP_DIR` with `/mnt/c/Users/<windows user>/Documents/Stockroom Backups`. Get the Windows user name from `cmd.exe /c echo %USERNAME%`. The folder picker (`local_folders.go`) lists that Documents folder as a place. A teacher can then find backups in Explorer, and they survive the distribution being removed. The database, uploads and photo mirror stay on the Linux side, which is much faster.
- [ ] **Clock.** `doctor` compares the WSL clock with Windows's. The Windows machine should be set never to sleep.
- [ ] **Kiosk.** Document starting Edge in kiosk mode at login (`msedge --kiosk http://localhost:8080 --edge-kiosk-type=fullscreen`) through a Startup-folder shortcut.
- [ ] **Clean install and reboot test on a real Windows 11 machine**, by hand. GitHub's Windows runners can't run WSL. Reboot without logging in, then log in and confirm Stockroom answered from boot: check `journalctl -u stockroom` inside WSL for the start time.
- [ ] **Decide what happens to native Windows development leftovers.** Keep `scripts/dev.ps1` and the `tests-windows` CI job as a development convenience, or delete them. Record the decision in `docs/decisions.md`, and update `CI.md` and the branch-protection check list to match.

# Not planned

- A native Windows installer, service, binary, winget or Scoop package. WSL covers Windows.
- The closet camera under WSL or on native Windows.
- Code signing on any platform.
- Homebrew core. It needs a well-known project and a source build; the tap is one command anyway.
- Embedding PostgreSQL in the binary. It downloads binaries at first run and makes the binary own major-version upgrades.
- Configurable product name, logo or colours.
- A `member_noun` setting; the product says "student" throughout.
- `SECURITY.md`. Not worth it at this scale (decided 2026-09-23).
- Multiple departments in one install. Use one install per department.
- Category trees deeper than three levels. Assets can file at any node (`docs/adr/0001`).
- Scan-to-create in the admin catalogue, and a column-mapping or dry-run step for CSV import.
- An in-app updater or tray icon. Installing the newer package is the upgrade.
