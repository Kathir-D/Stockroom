# Closet camera and activity log

ROADMAP section 2, built 2026-09-26 for macOS and Linux. Windows waits on the school choosing the closet PC's operating system.

This is the reference for how the camera, the visit recordings and the activity log fit together, what was measured, and why each decision went the way it did. CLAUDE.md §13 has the short version.

## 1. What it does

A USB webcam sits over the closet door facing in, so the frame is the room and nothing else. Frigate, an open-source person detector, watches it. Stockroom polls Frigate every five seconds and turns each tracked person into a **visit**: a walked-in row when Frigate starts tracking them, a walked-out row when it stops, the time in between, and a copy of the clip and snapshot in a folder on this machine.

Every other action at the closet goes into the same log: sign-ins and failed sign-ins, every barcode scan, checkouts, returns, damage notes, and admin changes. Admin → Activity reads it as one timeline. When an item goes missing, an admin opens its history, presses **Closet activity since its last return**, and sees who was in the room, who signed in and what was scanned in that window, with a play button on each visit.

Nothing links a face to an account. The timeline puts the camera's visits beside the sign-ins, and the admin draws the conclusion (ROADMAP 2.6).

## 2. Pieces

| Piece | Where | Job |
|---|---|---|
| Frigate 0.18 | `deploy/camera/docker-compose.yml`, one container, `127.0.0.1:5055` | Motion, then person detection, then event recording. Keeps its own copy of each clip for three days |
| go2rtc | macOS host only, `127.0.0.1:8554` | Reads the webcam through AVFoundation and serves RTSP to Frigate, because Docker Desktop cannot pass a USB device into a container |
| `camera.sh` | `deploy/camera/` | Renders Frigate's config for a source (`webcam` or `file:clip.mp4`), starts and stops the stack. `dev.sh camera` runs it; `install.sh --with-camera` installs it |
| Connector | `internal/stockroom/camera_detector.go` | The `Detector` interface and its Frigate implementation. Agent DVR would be a second implementation |
| Watcher | `camera_watch.go` | The poll loop: health, events, recordings, retention |
| Settings and visits | `camera.go` | `camera_settings`, `closet_visits`, keep flag, Test connection |
| Log | `activity.go` | `writeLog`, the timeline read, the CSV export, the unattended scan |
| UI | `screens/admin/activity.svelte`, `components/app/camera-settings-card.svelte`, `visit-*.svelte` | The timeline, the settings card, snapshots and the player |

## 3. Decisions

**Polled, not pushed.** Frigate can publish over MQTT, which needs a broker, a third container for a feed that changes a few times an hour. A poll every five seconds costs one loopback request and cannot lose an event: whatever happened while the server was down is still in Frigate's list when it comes back. The watcher asks for every person event since a minute before the newest visit on record, bounded at three days, and re-reads any visit still open that fell outside that window.

**Walked in is the event starting, walked out is it ending.** The camera faces into the closet from over the door, so a person appearing is someone arriving and the same tracked person leaving the frame is them going. No doorway zone is needed. Two people at once are two visits. A track shorter than `min_visit_seconds` (default 2) never becomes a visit, and a visit Frigate forgets (a detector restarted with an empty database) is closed with a row saying so, rather than leaving somebody "in the closet" forever.

**The detector's timestamps, not the poll's.** A walk-in row is timed at Frigate's `start_time`, not at the moment the watcher noticed. Frigate runs on the same machine, so its clock is the server's clock, and a five-second poll delay would otherwise misplace every visit against the sign-ins around it.

**Stockroom owns the recordings.** Frigate keeps three days. Stockroom copies each finished visit's clip and snapshot into the folder chosen in Admin → Settings and applies its own retention (default 30 days) and keep flag. That folder is never pushed to Drive or GitHub and never goes into a backup. `closet_visits` does go into the backup, because the log rows point at it, and on another machine its paths point at nothing. Paths are absolute, so changing the folder later does not orphan what is already recorded.

**A snapshot while the person is still inside.** An open visit gets its snapshot five seconds in, so the timeline shows who is in the room now and not only after they leave. The clip waits until twenty seconds after the end, when Frigate has finished the file.

**Three downloads per poll, each with its own two minutes.** Found live. The first run against a Frigate that had an hour of history queued fourteen clips at once, and a 20 MB clip does not fit inside a poll's 30-second budget. A backlog now drains a few clips per poll without holding up the walk-ins behind it. A visit that has used its twelve attempts drops out of the queue, so a clip Frigate never made cannot hold the head of it and stall every recording after it.

**Port 5055, not Frigate's 5000.** Found live. macOS's AirPlay Receiver (ControlCenter) listens on `*:5000`. While the Frigate container was stopped, AirPlay answered the watcher's polls with 403. The watcher reported "detector answered 403" instead of "detector is down", and a pooled keep-alive connection went on reaching AirPlay after Frigate came back, so the camera never returned to online on screen. Three fixes. The host port is 5055. The detector client has its own transport with keep-alives off, since a fresh loopback connection every five seconds costs nothing. And a non-Frigate answer is now named: "something that is not Frigate is answering at …".

**The detector must be on this machine.** Frigate's API port has no authentication, so the only access control is the `127.0.0.1` binding. `normalizeDetectorURL` refuses any host but loopback, so a typo cannot point the server at a machine on the school network.

**The log is append-only for everybody.** Triggers refuse `UPDATE`, `DELETE` and `TRUNCATE` on `activity_log` for every role, including the `postgres` superuser the server connects as. The one exemption is the restore, which reloads tables under `session_replication_role = replica`, where ordinary triggers do not fire. The restore then puts back every log row and closet visit the live database had that the archive did not, and writes its own row, so restoring last night's backup cannot erase today's log (§4).

**The log outlives what it talks about.** Both foreign keys on `activity_log` went. `asset_id` cascaded, so deleting an asset deleted its trail. `actor_id` had no action, so an account that appeared in the log could not be deleted. Each row snapshots the actor's name and the item's serial when it is written, and a one-line summary, so the CSV export and a restored backup read without the application.

**One row per action, in the action's transaction.** `writeLog` takes the transaction. A checkout writes one row per unit inside the checkout's transaction, so a refused cart leaves no row and a committed one cannot lack them. The status trigger from the base schema stays as a catch-all for hand-written SQL, and the server sets `stockroom.logged` in its own transactions so the trigger does not add a second row for the same change. Actions that write nothing else (a sign-in, a scan that only opened an item) write their row on its own, and a sign-in whose row cannot be written is refused.

A few rows are best effort, because refusing them would change nothing that already happened: sign-out, idle timeout, a camera going offline, and the summary rows for imports, which commit row by row.

**Every scan, with its screen.** The UI sends `X-Stockroom-Screen` on every request and the server puts it in the row. A scan that checks an item in is one `checkin` row marked `via_scanner`, carrying the code and the screen. A scan the sign-in screen rejects (an item barcode with nobody signed in) goes through `POST /signin/scan`, the only unauthenticated write besides the logins. It accepts three fixed outcomes and a 64-character code, and a token bucket (30, then one a second) bounds it.

**Full student numbers in the log.** Decided with the owner on 2026-09-26. A failed or unknown card scan keeps the exact code read. The log is admin-only and the backup already carries `accounts.csv`, and a number that almost matched somebody is exactly what an admin tracing a problem needs.

**Admin-only, enforced in the package.** `ListActivity`, `ExportActivityCSV`, `GetCamera`, `OpenVisitMedia` and `SetVisitKeep` all call `RequireAdmin`. Watching a clip writes a `recording_viewed` row. A snapshot does not, or opening the timeline would write a row per visit on screen. The player fetches the clip as a Blob because a `<video src>` cannot carry the bearer token. The server logs only the first request of a viewing (no `Range`, or one from byte 0), not every seek.

## 4. Restore

Before the truncate, the restore copies `activity_log` and `closet_visits` into temporary tables. After the load it inserts back every row whose `id` the archive does not have, records the counts in `RestoreResult.kept_newer`, and writes a `restore` row naming the archive's time and who ran it. All of this happens inside the restore's transaction, so a restore that fails keeps the live log exactly as it was.

## 5. Measured

Development machine: Apple M3 Pro, Docker Desktop, Frigate 0.18 with the CPU (TFLite) detector.

| | Value |
|---|---|
| Inference | 31 to 35 ms per detection |
| CPU, empty room | about 26% of one core, averaged over 18 samples |
| CPU, people in view | about 90% of one core, averaged over 28 samples |
| RAM | 1.3 GB for the container |
| Poll to walk-in row | under 5 s |
| Walk-out to saved clip | about 20 s |
| Clip size | about 0.16 MB per second at 640×512, 15 fps. A 125-second visit was 20 MB |
| Sign-in with the detector stopped | 53 ms |

The CPU numbers include ffmpeg decoding the looping sample file, so the idle figure is higher than a live camera's would be. On the closet PC, ROADMAP 2.1's hardware table still applies. An Intel machine gets the OpenVINO detector, which `camera.sh` picks automatically on Intel Linux.

Test footage is the EPFL CVLAB "laboratory" sequence (four people entering a room), padded with 25 seconds of empty room before and 45 after so each loop has walk-ins and walk-outs. Each loop produced the same fourteen visits with scores of 0.81 to 0.84, which makes it a usable regression check. §6 says how to build and run it.

## 6. Running it in development

```
./scripts/dev.sh camera up --source file:/path/to/clip.mp4   # sample footage
CAMERA_SIZE=1280x720 CAMERA_FPS=30 ./scripts/dev.sh camera up --source webcam --reconfigure   # a Mac's FaceTime camera
./scripts/dev.sh camera status
./scripts/dev.sh camera down
```

Then Admin → Settings → Closet camera: choose a recordings folder, tick **Record closet visits**, save. **Test connection** reports the Frigate version, the frame rate and the detection time.

The sample clip used above was built from `4p-c0.avi` by freezing its first frame for 25 s before and 45 s after, scaled to 640×512 at 15 fps (`ffmpeg -loop 1 -t 25 -i first.png -i 4p-c0.avi -loop 1 -t 45 -i first.png -filter_complex "...concat=n=3..."`).

On macOS the webcam path needs `ffmpeg` (Homebrew) and the `go2rtc` binary from its GitHub releases in `~/.local/bin`. macOS asks once for camera access for the app that starts go2rtc, and until someone allows it the capture hangs without an error.

## 7. Still open

- The live webcam has not run through go2rtc on this machine, because macOS's camera permission prompt needs a person to click it. The sample-footage path exercises the same Frigate, the same connector and the same watcher.
- The QuickCam Pro 9000 and the closet PC are not here yet (ROADMAP §4), so the tuning for them (960×720 MJPEG, 15 fps, OpenVINO) is written but unmeasured.
- The school's approval and the signage (ROADMAP 2.6).
- Windows, and Agent DVR as its detector.
- `dev.ps1` has no `camera` subcommand.
