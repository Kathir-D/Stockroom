# Roadmap

Open work only, in priority order. Completed work and the reasoning behind it are recorded in [`CLAUDE.md`](CLAUDE.md) §13.

# Priority

The two features being built next.

## 1. Sign-in photo wall

**Goal:** point the wall at the student media photos folder in Google Drive. Stockroom picks about 150 photos from it (the number is a setting, not fixed) and scrolls them past as film strips on either side of the sign-in card.

**What exists today** ([`docs/design/signin-photo-wall.html`](docs/design/signin-photo-wall.html)):

- Admin → Photo wall connects a Drive folder, checks the link against Drive before saving, and shows a preview.
- The server lists the folder weekly, downloads photos one at a time and crops each to a 900×600 JPEG with metadata removed.
- Photos are single-use. The server keeps 48 ready, hands out 32 per sign-in page load and deletes each one 15 minutes after showing it.
- Verified against a real folder (2,274 files, 2,073 usable) on the development machine.

**What changes.** The wall moves from single-use tiles to a set of about 150 photos held in memory, shown as a film strip.

### 1.1 Photo set

- [ ] Replace the single-use reel with a **photo set** of N photos (default 150), prepared once and cycled continuously.
- [ ] **Pick randomly across folders, with a per-folder cap.** Choose folders first and then a photo within each, or cap any one folder at a small share of the set, so a folder of 200 photos from one event cannot fill the wall by chance.
- [ ] **Keep photos in memory, not on disk.** 150 tiles at about 100 KB each is roughly 15 MB of RAM. Tiles are served from memory and never written to the machine, and the set is rebuilt after a restart. Only fall back to a disk cache if measurements show the rebuild costs too much bandwidth or time.
- [ ] Refresh the set gradually: swap one photo at a time for a new random pick on a slow timer, so the strip changes over the day without re-downloading everything.
- [ ] Drop a photo when it disappears from the Drive folder at the next listing.
- [ ] Fill the set in the background. The strip shows whatever is ready and grows as more photos arrive.
- [ ] Keep the existing guards: one download at a time with a delay, the same rclone remote and quota as the backup, and no effect on sign-in if Drive is unreachable.

### 1.2 Faster filling

At the measured rate of one photo every 20 to 25 seconds, filling 150 takes about an hour, and that repeats after every restart while photos are kept in memory.

- [ ] Download by file ID instead of by path, so rclone does not resolve every folder on each fetch.
- [ ] Skip portrait and panoramic photos using image dimensions from the listing where Drive provides them, instead of downloading the full file and rejecting it.
- [ ] Measure the fill time and total download size against the real folder, and use that to confirm memory-only is acceptable.

### 1.3 Film strip on the sign-in screen

- [ ] Restyle the columns as **film strips**: photo frames with sprocket-hole edges, scrolling continuously at a steady, slow pace.
- [ ] Cycle through the whole set rather than one batch of 32. Load images lazily as they approach the visible area, and add new photos to the end of the strip so there is no visible jump.
- [ ] Replace the batch endpoint with one that returns the current set. It stays unauthenticated and returns only tile URLs.
- [ ] Keep the current behaviour when the set is empty or the feature is off: no strips, and the sign-in field is unaffected. Respect reduced-motion settings.

### 1.4 Admin

- [ ] Set size in Admin → Photo wall (default 150), with an upper limit to bound memory use.
- [ ] Show fill progress, such as "112 of 150 ready".
- [ ] Add a **Reshuffle now** button that picks a new set.
- [ ] Remove the `.env` settings the new model replaces (`SIGNIN_PHOTOS_BATCH`, `SIGNIN_PHOTOS_TTL_MINUTES`, `SIGNIN_PHOTOS_DIR`, and `SIGNIN_PHOTOS_COUNT` once it is a panel setting).

### 1.5 Tests and rollout

- [ ] Go tests for folder-balanced selection, the per-folder cap, gradual refresh, removal of deleted photos, folder switching, and the memory limit.
- [ ] Update the frontend smoke tests for the new endpoint.
- [ ] Manual checks: unplug the network and confirm sign-in is unaffected, and watch a full cycle in the Wails window on the slowest machine available.
- [ ] Set up the read-only rclone remote on the closet PC and connect the student media photos folder (after the PC is available, see section 4).
- [ ] Update the design doc, `CLAUDE.md` §8.1 and §13, and `.env.example`.

## 2. Closet camera and activity log

**Goal:** a USB webcam watches the equipment closet. Stockroom records when someone walks in and walks out, saves the video of each visit locally, and logs every account, scanner and equipment action with a timestamp. If an item goes missing without being checked out, admins can read one timeline, see who signed in, what was scanned and checked in and out, and watch the video of who was in the room.

**Approach:** use an existing open-source detector. Stockroom does not implement person detection itself; it only reads the detector's events.

### 2.1 Person detection

The detector is chosen by the closet PC's operating system, which the school has not confirmed yet. No free, open-source option runs natively on Windows.

| Option | Runs on | Licence and cost | Notes |
|---|---|---|---|
| **[Frigate](https://github.com/blakeblackshear/frigate)** (preferred) | Linux; Windows and macOS only through Docker Desktop, which Frigate does not support | MIT, free | Most efficient. Runs one Docker container next to `postgres:17` |
| **[Agent DVR](https://www.ispyconnect.com/)** (Windows fallback) | Windows natively, also macOS and Linux | Closed source. Free for private use only; a school needs a subscription (from $7.95/month), and its AI detection requires one | Local USB cameras, built-in AI detection with a small model for low-end hardware, GPU on Windows, event recording, API and webhooks, fully offline |

Considered and not chosen: [Viseron](https://github.com/roflcoopter/viseron) and [OpenNVR](https://github.com/open-nvr/open-nvr) (both Docker-only, with the same Windows limits as Frigate), and [CodeProject.AI Server](https://github.com/codeproject/CodeProject.AI-Server) (Windows-native, but only a detection engine, so recording and entry/exit tracking would have to be built).

**Decision:**

1. **Linux on the closet PC:** use Frigate.
2. **Windows, subscription approved:** use Agent DVR.
3. **Windows, no subscription:** run Frigate in Docker Desktop, with [go2rtc](https://github.com/AlexxIT/go2rtc) on the host to supply the webcam, and prototype it on the actual PC before committing.

Stockroom only reads the detector's events and recordings, behind one small connector, so the log, timeline and recording features below are the same whichever is chosen.

**Why Frigate is efficient:**

- **Motion first, ML second.** Cheap motion detection runs on every frame, and the person detector only runs on the regions where motion was found. An empty closet costs almost nothing.
- **Small model.** Detection runs on a 320×320 input (MobileNet-class models), about 15 ms per inference on an Intel N100 with OpenVINO.
- **CPU-only works on Intel.** Its OpenVINO detector runs on the CPU or integrated GPU with no graphics card. A Google Coral USB accelerator (about 10 ms per inference) can be added later if the PC is too slow.
- **Event recording.** It records only while a tracked object is present, with a few seconds before and after, and deletes recordings after a set number of days.
- Tracks each person as an event with a start time, end time and snapshot, supports zones (for example the doorway), and exposes events over an HTTP API.

Agent DVR has the same shape: a Small model for low-end hardware, detection triggered by motion, and a configurable detection rate.

**Minimum hardware** for Stockroom, PostgreSQL and the detector with one camera:

| Component | Minimum | Recommended |
|---|---|---|
| CPU | 64-bit x86 with **AVX and AVX2** (Frigate requires both; most Intel and AMD CPUs from 2013 onward). Intel preferred, for OpenVINO | Intel N100 or newer, or a 6th-generation Core i3 or better |
| RAM | 8 GB total (Frigate needs 4 GB, the rest is Stockroom, PostgreSQL and the OS) | 16 GB |
| Storage | SSD with 50 GB free for recordings | SSD with 100 GB or more free |
| Operating system | Linux for Frigate, Windows 10 or later for Agent DVR | Linux, e.g. Ubuntu LTS |
| Camera | Any UVC USB webcam | **Logitech QuickCam Pro 9000** (planned), see the note below |
| Accelerator | None | Google Coral USB (Frigate), if detection cannot keep up on the CPU |

**Camera: Logitech QuickCam Pro 9000.** A UVC webcam, so it needs no drivers on Linux or Windows. It outputs MJPEG up to 960×720 at about 30 fps, or uncompressed YUYV up to 1600×1200 at low frame rates. It has **no built-in H.264**, so the detector has to encode recordings itself. To keep that cheap:

- Capture MJPEG at 960×720 (or 800×600), and record at 10 to 15 fps rather than 30.
- Use hardware encoding (Intel Quick Sync) where the PC and OS allow it; otherwise software encoding at 720p and 15 fps is a light load on any CPU that meets the minimum.
- Detect on a 640×360 downscale at about 5 fps.
- Its field of view is narrow compared with modern wide-angle webcams, so mount it where it sees the doorway and the shelves in one frame.

Storage estimate: 960×720 at 15 fps is about 1 Mbps, roughly 0.5 GB per hour of recorded video. Two hours of closet activity a day kept for 30 days is about 30 GB.

Tasks:

- [ ] Confirm the closet PC's operating system with the school, and pick the detector from the table above.
- [ ] Prototype the chosen detector with the QuickCam Pro 9000 on the development machine: detect a person entering and leaving, and measure CPU and RAM use at idle and with a person in view.
- [ ] Check the closet PC against the minimum hardware table once it arrives (CPU model and AVX2 support, RAM, free disk, operating system).
- [ ] Tune for low-end hardware: detect at about 5 fps at 640×360, use the Small model (Agent DVR) or the OpenVINO detector (Frigate), capture MJPEG at 960×720, and encode recordings at 10 to 15 fps, with hardware encoding where available. Measure the CPU cost of encoding separately from detection.
- [ ] **Frigate on Windows or macOS only:** Docker Desktop cannot pass a USB webcam into a container, so run go2rtc on the host to serve the webcam as a local RTSP stream. On Linux the device is mapped directly.
- [ ] Add the detector to the install (`deploy/docker-compose.yml` for Frigate, or install steps for Agent DVR in `docs/INSTALL.md`), listening on `127.0.0.1` only. Make the camera optional: no camera configured means none of this runs.
- [ ] Record a fallback if the closet PC cannot run person detection: motion-only detection logged as "activity in the closet".

### 2.2 Entry and exit events in Stockroom

- [ ] A Go goroutine in the server reads new person events from the detector: Frigate's HTTP API (polled, so no MQTT broker is needed) or Agent DVR's API and webhooks. Keep this behind one small connector interface.
- [ ] Record **walked in** when a person event starts and **walked out** when it ends, using a doorway zone to tell arrivals from people already in the room.
- [ ] Store a snapshot per event.
- [ ] If the detector or the camera is down, sign-in and checkout keep working, and the admin panel shows the camera as offline, with the gap recorded in the log.

### 2.3 Visit recordings

- [ ] **Save video from when a person walks in until they walk out**, plus a few seconds before and after, using the detector's event-based recording. No video is kept while the closet is empty.
- [ ] Store recordings **locally only**, in a folder set in Admin → Settings. They are never pushed to Google Drive or GitHub and are left out of backups.
- [ ] Link each recording to its walked-in and walked-out log entries, and play it from the admin timeline.
- [ ] Delete recordings automatically after a retention period set in the admin panel (for example 30 days), and warn on the backup-style warning surfaces when free disk space runs low.
- [ ] Let an admin mark a recording **keep**, so evidence for an open investigation is not deleted by retention.
- [ ] Estimate disk use from the prototype: minutes of video per school day multiplied by the camera's bitrate.

### 2.4 Complete activity log

Every event goes into one append-only log. **Every entry has a timestamp** (taken from the server clock, stored with time zone), the account when there is one, and the details. Today, only asset status changes are logged (`activity_log` via a trigger).

- [ ] Closet: walked in, walked out (with the linked recording), camera offline and online.
- [ ] Accounts: sign-in by scan, sign-in by password, failed sign-in, first password set, sign-out, idle timeout.
- [ ] **Barcode scanner: every scan**, with its timestamp, the code read, the screen it was scanned on, who was signed in, and the result (signed in, checked in, opened item, unknown code, refused). This includes scans with nobody signed in and scans that did nothing.
- [ ] Equipment: checkout (with custodian and due date, and whether an admin override was used), check-in, damage note, kit check-in, item status changes.
- [ ] Admin: user, asset, category and kit changes, password resets, imports, settings changes, backup, restore, export.
- [ ] Log rows cannot be edited or deleted through the app, and a restore does not erase log entries written after the backup was taken.
- [ ] Record log entries inside the same transaction as the action, so an action cannot happen without its log entry.

### 2.5 Admin timeline

- [ ] **Admin → Activity**: one timeline of all events in time order, filterable by date and time range, person, item and event type. **Admins only**; the API refuses everyone else, and viewing a recording is itself logged.
- [ ] Show each visit (walked in to walked out) with its snapshot and a play button for the recording, next to the sign-ins, scans and checkouts that happened during it.
- [ ] Item view: from an item's history, jump to closet activity between its last check-in and when it was reported missing.
- [ ] Export a time range as CSV.

### 2.6 Decisions to record

- [ ] **Privacy and consent.** Recording students on video needs school approval. Decide on signage, and how long recordings, snapshots and log entries are kept.
- [ ] Log rows are included in backups; recordings and snapshots are not.
- [ ] Linking a person event to an account is out of scope. The timeline places sign-ins and camera events side by side, and an admin draws the conclusion.

### 2.7 Tests and rollout

- [ ] Go tests with a fake detector API: event polling, enter/exit mapping, recording links, retention and the keep flag, camera outage handling.
- [ ] Tests that each logged action, including every scan, writes exactly one timestamped log row, that the log is not editable, and that non-admins cannot read it.
- [ ] On the closet PC: mount the camera with a clear view of the door, draw the doorway zone, and measure CPU use over a full school day.

# Other work

## 3. Before v1.0

- [ ] **Windows installer and service.** `install.ps1` is not started. `dev.ps1` has never run on real Windows hardware; CI only parses it.
- [ ] **Install from a release binary.** `install.sh` builds from a checkout, so it needs Go and Node. `.github/workflows/release.yml` builds the binaries, but no release has been tagged or tested.
- [ ] **Install Docker when it is missing**, instead of only checking for it.
- [ ] **Reboot test.** Reboot an installed machine and confirm automatic login, Docker, the database and the service all come back, and the nightly backup still runs. Offer this check at the end of the install.
- [ ] **Clean-machine install.** Follow `docs/INSTALL.md` on a machine with nothing preinstalled, time it, and fix every place the docs fall short.
- [ ] **Full checkout on that machine:** import a roster, categories and assets, print labels, scan a card and an item, check out and back in.
- [ ] **Usability test** with someone who has not seen Stockroom: time to first checkout, and how often they needed help.
- [ ] **Pilot** in one department for a term.
- [ ] **Tag `v1.0.0`** once the clean-machine install passes.

## 4. Hardware and closet PC

Blocked: waiting on the school to provide the closet PC and the barcode scanner.

- [ ] Get the Logitech QuickCam Pro 9000 for the closet (section 2) and test it with the chosen detector on the development machine.
- [ ] Confirm the closet PC meets the minimum hardware in section 2.1 (AVX2, 8 GB RAM, 50 GB free on an SSD). Ask the school whether it can run Linux (Frigate) or must stay on Windows (Agent DVR, or Frigate in Docker Desktop).
- [ ] Tune `SCAN_KEY_THRESHOLD_MS` (currently 50 ms) with Ctrl+Shift+D once the scanner arrives.
- [ ] Provision the closet PC: backup folder, photo mirror disk and off-site credentials.
- [ ] Decide whether the photo mirror gets a second physical disk. It is currently the only copy of item photos besides `uploads/`.

## 5. Backups

- [ ] Test the GitHub target against a real private repository, then run it alongside Google Drive.
- [ ] Support a custom Google OAuth client ID for Drive: rclone's shared client ID is being retired during 2026. The consent screen must be published, not left in Testing, or tokens expire after seven days (see `CLAUDE.md` §13, "Still open").
- [ ] Configure everything from a fresh install without opening a text editor.

## 6. Repository

- [ ] Branch protection on `main`, requiring `tests` and `tests-windows` (see [`CI.md`](CI.md)).
- [ ] Repository description and topics: `school`, `inventory`, `checkout`, `barcode`, `education`, `equipment`, `go`, `svelte`, `self-hosted`.
- [ ] Screenshots or a short recording in the README, with the example data loaded.
- [ ] Make `./scripts/dev.sh test` repeatable: a second run without `supabase db reset` fails `080_seed.test.sql`, because the Go suite leaves the database changed.
- [ ] Fix the `svelte-check` warning in `category-tree.svelte` (`openPath` captured by value).

## 7. Later

- [ ] Settings for the remaining hardcoded rules: maximum checkout length (7 days, read by both the server and the date picker), whether overdue blocks checkout, the session idle timeout, and the scan threshold.
- [ ] `docs/HARDWARE.md`: scanner buying guide and physical setup (placement, counter height, sleep settings).
- [ ] `docs/UPGRADING.md` once there are two versions.
- [ ] `docs/FAQ.md`.
- [ ] Webcam capture and drag-and-drop for asset photos.
- [ ] Assign student numbers from a range for groups without ID numbers.
- [ ] Add users by pasting a list of names, without a CSV.
- [ ] `stockroom doctor`: check the Docker daemon, what holds port 8080, and disk space.
- [ ] A support bundle: doctor output and log tails, with secrets removed.
- [ ] Uninstall instructions.
- [ ] A data-deletion flow for students beyond deleting the account.

# Not planned

- Configurable product name, logo or colours
- A `member_noun` setting; the product says "student" throughout
- `SECURITY.md`; not worth it at this scale (decided 2026-09-23)
- Multiple departments in one install (use one install per department)
- Category trees deeper than three levels (assets can file at any node, see `docs/adr/0001`)
- Scan-to-create in the admin catalogue, and a column-mapping or dry-run step for CSV import
- An in-app updater or tray icon; re-running the installer is the upgrade
