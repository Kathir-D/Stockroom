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

## A2. Sign-in photo wall: what's left

The film strip and the in-memory set are built (`docs/decisions.md`, 2026-09-27). What's left needs the real Drive folder or the closet PC.

- [ ] **Fill faster.** A real fetch takes 20 to 25 seconds, so about an hour for 150 photos after every restart.
  - Download by file id rather than by path, so rclone doesn't resolve every folder on each fetch. Try `rclone backend copyid` and `--drive-root-folder-id` set to the file's parent, and measure both.
  - Check whether `rclone lsjson --metadata` returns Drive's image width and height. If it does, skip portrait and panoramic photos without downloading them.
  - Measure fill time and total download size against the real folder.
- [ ] **By hand**, unplug the network and confirm sign-in is unaffected, then watch a full cycle of the strips on the slowest machine available.
- [ ] **On the closet PC**, press **Sign in with Google** in Admin → Photo wall, then **Choose from Google Drive** and pick the student media photos folder. Blocked on section 8.

## A3. Repository

- [ ] **Branch protection on `main`**, requiring `tests`, `tests-windows`, the four `tests-postgres` jobs and the two `package-linux` jobs. [`CI.md`](CI.md) has the exact `gh api` command. The owner has to run it, or an agent with admin rights on the repository.
- [ ] **Add screenshots or a short recording to the README**, with the example data loaded (setup wizard → Load examples). Show sign-in, browse, the cart, and Admin → Activity. Put the images in `docs/images/`.

## A4. Product work for later

None of this is needed for v1.0. Pick any of it up when track B is waiting on hardware or people.

- [ ] **`docs/UPGRADING.md`**, once there are two released versions.
- [ ] **Asset photos from a webcam, and by drag and drop.**
- [ ] **Assign student numbers from a range** for groups that have no ID numbers.
- [ ] **Add users by pasting a list of names**, without a CSV.
- [ ] **A data-deletion flow for a student**, beyond archiving or deleting the account. The activity log is append-only by design (`docs/adr/0003`), so this needs a decision first.

# Track B: the main path to production

Do these in order. Each section's dependencies are listed at its start.

## 1 to 4. The binary, the package, releases, doctor

Done on `feat/roadmap-production-path`, except what needs a tag or a real machine. `docs/decisions.md` (2026-09-27, one binary, the packages, setup and doctor) records what was decided. What's left:

- [ ] **Test Google sign-in on rclone 1.60 against a real account.** Every rclone command Stockroom runs works on 1.60.1 offline. The browser flow through `rclone authorize` with the school's client id hasn't been tried. If it fails, the `.deb` moves rclone to `Recommends` and `get.sh` installs rclone.org's own `.deb`.
- [ ] **Create the tap and its token.** The owner creates the repository `Kathir-D/homebrew-stockroom` and a fine-grained token with contents write on it only, stored in this repository as `HOMEBREW_TAP_TOKEN`. `release.yml` commits the cask there on each full release.
- [ ] **Release a candidate.** Tag `v0.9.0-rc.1`. The release workflow marks it a prerelease, so it doesn't touch the tap and `releases/latest` skips it. Install it with `curl … get.sh | sudo STOCKROOM_VERSION=v0.9.0-rc.1 bash` on a machine that isn't the development Mac.

**Done when:** a pushed tag produces a GitHub release with the archives, both `.deb` files, `checksums.txt`, and a cask commit in the tap.

## 5. Prove it on Linux

Depends on the candidate release. Linux is ready for the closet PC once this passes.

The `package-linux` CI job (`CI.md`) installs the `.deb`, runs setup, restarts the service, upgrades to a package with one more migration, and runs `doctor`, on the ubuntu-24.04 runner with systemd and in a debian:12 container. The same steps passed locally in containers, the systemd one included.

- [ ] **Clean-VM run by hand.** Fresh Ubuntu 24.04 desktop VM. Run the one-line install and time it. In the web wizard, load nothing, import `examples/` files through the real screens, and print labels to PDF. Connect Google in Admin → Settings and choose a Drive backup folder. Do one checkout and one return with a keyboard standing in for the scanner. Write down every place the docs fell short and fix them.
- [ ] **Reboot test.** Reboot and don't log in. From another machine or a console, confirm that Postgres and Stockroom are running and `/health` answers. Log in and confirm the nightly backup's boot catch-up ran: it logs "running now" when the last success is stale. Setup prints this check as its last step.
- [ ] **Power-cut test.** Hard-power-off the VM mid-use. It comes back with no data lost that was committed before the cut.

**Done when:** all three pass, `package-linux` is green on `main`, and the fixes are merged.

## 6. macOS

Depends on the tap.

`stockroom setup` on macOS is written and unit-tested: data and config in `$(brew --prefix)/var/stockroom`, `initdb` if the cluster is missing, Homebrew's own `postgresql@17` agent stopped, and two LaunchDaemons (`com.stockroom.postgresql`, `com.stockroom.server`) with `UserName`, `RunAtLoad` and `KeepAlive`. It has never run, because it needs `sudo` on a real Mac.

- [ ] **Prove it on a real Mac.** Install from the tap, run setup, reboot without logging in, and confirm `/health` answers from another machine through SSH port forwarding, or check `$(brew --prefix)/var/log/stockroom.log` after logging in. Check the cask's post-install hook cleared the quarantine flag (`xattr -l $(brew --prefix)/bin/stockroom` shows nothing). If boot-time start can't be made to work, fall back to automatic login (System Settings → Users & Groups) and record why in `docs/decisions.md`.
- [ ] **Delete the old installer** (decision 5) once this and section 5 pass: `scripts/install.sh`, `deploy/docker-compose.yml`, `deploy/stockroom-run.sh`, `deploy/com.stockroom.server.plist.template` and `deploy/stockroom.service.template`. Remove the "Installing from a checkout" section of `docs/INSTALL.md` and every other mention. Keep `deploy/camera/`. On the development Mac, move the existing Docker-based install over with Admin → Backup → Export everything, then restore it on the new install.

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
- [ ] **Kiosk browser.** Set it up as `docs/INSTALL.md` (The kiosk screen) describes, and fix the guide where the real PC differs.
- [ ] **Buy a plain USB HID keyboard-wedge barcode scanner** that sends Enter after each code. Tune the scanner speed in Admin → Settings (50 ms by default) with the Ctrl+Shift+D diagnostic, set the default in `scanner.ts` and the migration to match, and record the model and value in `CLAUDE.md` §10.
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

Stockroom runs inside WSL 2's Ubuntu 24.04 exactly as on Linux: the same `.deb`, the same `setup`, the same systemd unit. The browser, the barcode scanner and the label printer stay on the Windows side.

Written, and never run on Windows, because nothing here has one:

- `scripts/get.ps1` checks it runs elevated, installs Ubuntu 24.04 if missing (its first start asks for the Linux account that will run Stockroom), turns systemd on, runs `get.sh` inside it, and registers the `Stockroom WSL` boot task, which runs `wsl.exe -d Ubuntu-24.04 --exec /usr/bin/sleep infinity` whether or not anyone is logged on. It parses under PowerShell 7.
- Under WSL, setup seeds `BACKUP_DIR` with `Documents/Stockroom Backups` in the Windows profile, and the folder picker lists the Windows Documents folder.
- `doctor` checks systemd, the boot task, and the clock against Windows's.
- `docs/INSTALL.md` covers the Windows install and the Edge kiosk shortcut.

- [ ] **Clean install and reboot test on a real Windows 11 machine**, by hand. GitHub's Windows runners can't run WSL. Run `get.ps1` on a machine that has never had WSL, including the restart Windows asks for. Reboot without logging in, then log in and confirm Stockroom answered from boot: check `journalctl -u stockroom` inside WSL for the start time. Fix `get.ps1` where it breaks.
- [ ] **Check the Windows browser reaches the server** at `http://localhost:8080` through WSL's localhost forwarding. Test with the default networking and with `networkingMode=mirrored` in `.wslconfig`. The server's loopback `Host` check (`withHostCheck` in `server/router.go`) already accepts `localhost`.
- [ ] **Test Google sign-in early.** `rclone authorize` listens on `127.0.0.1:53682` inside WSL, and Google redirects the Windows browser there. If forwarding doesn't carry it, the paste-a-code fallback in the sign-in dialog ("Signing in on a different computer?") has to work, and the docs have to point at it.
- [ ] **Decide what happens to native Windows development leftovers.** Keep `scripts/dev.ps1` and the `tests-windows` CI job as a development convenience, or delete them. The owner decides. Record it in `docs/decisions.md`, and update `CI.md` and the branch-protection check list to match.

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
