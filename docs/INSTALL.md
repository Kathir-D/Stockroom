# Installing Stockroom

This guide covers the install on the machine that runs Stockroom day to day. For a development environment, see [CONTRIBUTING.md](../CONTRIBUTING.md).

An install is the operating system's PostgreSQL, one `stockroom` binary and one service that starts it at boot. The binary holds the API, the web UI and every database migration. Setup puts PostgreSQL on loopback only and creates a database that holds nothing else.

| Platform | Command |
|---|---|
| Debian 12 or Ubuntu 24.04, and newer | `curl -fsSL https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.sh \| sudo bash` |
| macOS | `brew install kathir-d/tap/stockroom && stockroom setup` |
| Windows 10 or 11 | `get.ps1` in an administrator PowerShell, which runs the Linux package inside Ubuntu under WSL 2 |

There is no native Windows version.

## Linux

```bash
curl -fsSL https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.sh | sudo bash
```

`get.sh` downloads `stockroom_amd64.deb` or `stockroom_arm64.deb` from the latest GitHub release, checks it against the release's `checksums.txt`, and installs it with apt. apt pulls in PostgreSQL (14 or newer), its client tools and rclone (1.60 or newer). Ubuntu 22.04 packages rclone 1.53, so `get.sh` stops there with a message. Then it runs `stockroom setup`, which asks for a failsafe admin and prints the address when Stockroom answers.

To install a particular release, set `STOCKROOM_VERSION`:

```bash
curl -fsSL https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.sh | sudo STOCKROOM_VERSION=v1.0.0 bash
```

Arguments after `bash -s --` go to setup, for example `… | sudo bash -s -- --service-user stockroom`. You can also download the `.deb` from the releases page, `sudo apt install ./stockroom_amd64.deb`, then `sudo stockroom setup`.

The service runs as the account that ran `sudo`, so the backup folder picker starts in that person's home and offers their Documents folder and USB drives. On a headless server, `--service-user stockroom` creates a system account instead.

| Path | What |
|---|---|
| `/usr/bin/stockroom` | the binary |
| `/etc/stockroom/stockroom.env` | the config, mode 600. It holds the database password |
| `/var/lib/stockroom/uploads` | item and profile photos |
| `/var/lib/stockroom/backups` | nightly backups, and `pre-migrate/` dumps taken before each upgrade |
| `/var/lib/stockroom/photo-backups` | the photo mirror |
| `/lib/systemd/system/stockroom.service` | the unit. Setup adds `User=` in `/etc/systemd/system/stockroom.service.d/user.conf` |

## macOS

```bash
brew install kathir-d/tap/stockroom
stockroom setup
```

Homebrew installs `postgresql@17` and rclone with it. Run setup as yourself, not with `sudo`. It asks for your password once, to install two LaunchDaemons: `com.stockroom.postgresql` and `com.stockroom.server`. Both start at boot with nobody logged in and run as you. If Homebrew's own `brew services` agent for `postgresql@17` is running, setup stops it, so two copies don't fight over the data folder.

| Path | What |
|---|---|
| `$(brew --prefix)/var/stockroom/stockroom.env` | the config, mode 600 |
| `$(brew --prefix)/var/stockroom/` | `uploads`, `backups`, `photo-backups` |
| `$(brew --prefix)/var/log/stockroom.log` | the server's log |
| `/Library/LaunchDaemons/com.stockroom.*.plist` | the two daemons |

The binary isn't signed. The cask removes macOS's quarantine flag from it after install so it runs without a warning.

## Windows (WSL)

Open PowerShell as administrator (right-click, Run as administrator) and run:

```powershell
irm https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.ps1 | iex
```

`get.ps1`:

1. installs Ubuntu 24.04 under WSL if it's missing. On a first WSL install Windows may ask for a restart. Restart and run the command again. Ubuntu then asks for a new user name and password, and that account runs Stockroom.
2. turns systemd on in the distribution (`[boot] systemd=true` in `/etc/wsl.conf`).
3. runs `get.sh` inside Ubuntu, which is the same package and setup as on Linux.
4. registers a Task Scheduler task, `Stockroom WSL`, that starts Ubuntu at boot and keeps it running. It asks for your Windows password so the task runs with nobody logged in.

Open `http://localhost:8080` in any Windows browser. The barcode scanner and the label printer stay on the Windows side and need nothing extra. Backups go to `Documents\Stockroom Backups` in your Windows profile, where Explorer finds them and where they survive the Ubuntu distribution being removed. The database and photos stay inside Ubuntu.

Set the PC never to sleep (Settings, System, Power). WSL's clock can drift after a sleep, and `stockroom doctor` inside Ubuntu checks it against Windows's.

The closet camera doesn't run under WSL.

## Setup options

`stockroom setup` is safe to run again. A second run finds the database, the config and the service in place and repairs only what's missing. It never overwrites the config.

| Flag | Description |
|---|---|
| `--admin-number <n>` | failsafe admin student number |
| `--admin-password-file <file>` | file holding the failsafe admin password. `-` reads stdin |
| `--non-interactive` | fail instead of asking anything |
| `--service-user <name>` | Linux: the account the service runs as. `stockroom` creates a system account |
| `--addr <host:port>` | listen address. Default `127.0.0.1:8080` |
| `--config <file>` | where to write the config and point the service at it. Default: the one the service already reads, else the system path. The path can't contain spaces, quotes or `$ % ; \` `` ` `` |
| `--no-service` | don't install or start the service, for a machine without systemd |
| `--no-open` | don't open a browser at the end |
| `--with-camera` | also start the closet camera's detector, which needs Docker |

There is no flag for the password itself, because other users can read command-line arguments and shells save them in history.

### Failsafe admin

The failsafe admin is recreated with admin rights and its configured password on every start. It's the way back in after a forgotten password, or after restoring into an empty database. It's optional. Without it, admins see a warning at sign-in.

### Closet camera

Optional. `setup --with-camera` checks that Docker is installed and running, then starts Frigate, an open-source person detector, in a container on `127.0.0.1:5055` reading a USB webcam. Setup never installs Docker. On Linux the camera (`/dev/video0`) goes straight into the container. On macOS Docker can't reach a USB camera, so go2rtc runs on the Mac and passes the video to Frigate. That needs `ffmpeg` (`brew install ffmpeg`) and the `go2rtc` binary from its [releases page](https://github.com/AlexxIT/go2rtc/releases) in `~/.local/bin`. The first time go2rtc starts, macOS asks for camera access. Allow it, or the camera stays dark without an error.

Installing it records nothing. Turn it on in **Admin → Settings → Closet camera**: choose a recordings folder (setup makes `recordings/` in the data folder), tick **Record closet visits**, and press **Test connection**. Recording students needs your school's approval and a sign on the door. The design and the measurements are in [design/closet-camera.md](design/closet-camera.md).

## First run

Open `http://127.0.0.1:8080`, or the address setup printed. The database starts empty, with no categories, equipment or accounts other than the failsafe admin.

The setup wizard opens on its own:

- If no failsafe admin was set, it asks you to create the first admin account.
- If one was set, sign in with it and the wizard opens.

The wizard covers the student-number format, lending rules, categories, equipment, the student roster and backups. It saves progress, so you can leave and come back. **Admin → Settings → Run the setup guide again** reopens it.

The [`examples/`](../examples/) folder has sample files for each import. **Admin → Assets → Print labels** and **Admin → Users → Print ID cards** produce PDFs to print at 100% scale.

### Setting up without the wizard

Every wizard step is also on the admin screens:

- Setup points backups at the `backups/` and `photo-backups/` folders. Google Drive and GitHub are set up in Admin → Settings ([`BACKUP-SETUP.md`](BACKUP-SETUP.md)).
- Import categories, equipment and students from Admin → Categories → Import, Admin → Assets → Import CSV and Admin → Users → Import roster. See [`ADMIN-GUIDE.md`](ADMIN-GUIDE.md).
- **Stop showing this guide**, at the bottom of each wizard page, marks setup as complete.

### Editing the config

After editing the config, restart the service with `sudo stockroom service restart` (Linux) or `stockroom service restart` (macOS).

| Value | Editable |
|---|---|
| `ADMIN_STUDENT_NUMBER`, `ADMIN_PASSWORD` | Yes. Applied on every start |
| `SESSION_IDLE_MINUTES` | Yes |
| `SERVER_ADDR` | Yes. The browser address changes to match |
| `PRE_MIGRATE_DUMP`, `PRE_MIGRATE_DIR`, `PG_DUMP`, `RCLONE_BINARY` | Yes. See `.env.example` |
| `BACKUP_DIR`, `PHOTO_BACKUP_DIR`, `RCLONE_REMOTE`, `SIGNIN_PHOTOS_FOLDER_ID` | No. Read on the first start only, then managed in the admin panel |
| `DATABASE_URL` | Only together with the database role's password |

Data paths in an installed config must be absolute. The server refuses to start on a relative one and names the variable. Leaving `UPLOADS_DIR` or `SIGNIN_PHOTOS_DIR` out is fine: they default to `uploads` and `cache` under `/var/lib/stockroom` (Linux) or `$(brew --prefix)/var/stockroom` (macOS).

## The kiosk screen

The server needs nobody logged in, but the screen students use does. Give the machine a local account that logs in automatically, stop it sleeping, and open Stockroom full-screen at login.

Ubuntu. In Settings, turn on automatic login for the kiosk account (Users) and set Blank Screen to Never (Power). Then save this as `~/.config/autostart/stockroom.desktop` in that account:

```ini
[Desktop Entry]
Type=Application
Name=Stockroom
Exec=chromium --kiosk --app=http://127.0.0.1:8080
```

Firefox works too: `firefox --kiosk http://127.0.0.1:8080`.

Windows. Set the PC never to sleep (Settings, System, Power). Press Win+R, type `shell:startup`, and in that folder make a shortcut whose target is:

```
"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --kiosk http://localhost:8080 --edge-kiosk-type=fullscreen
```

macOS. Turn on automatic login (System Settings, Users & Groups), and set the display never to sleep (Lock Screen). Add a login item that runs `open -a Safari http://127.0.0.1:8080`, then make the window full-screen.

Alt+F4 (Cmd+Q on a Mac) leaves kiosk mode.

## Upgrading

Run the install command again: `get.sh` on Linux and WSL, `brew upgrade stockroom` on macOS. Installing the newer package restarts the service. Before the new binary applies a migration, it dumps the database with `pg_dump` into `backups/pre-migrate/` (mode 600, newest 10 kept). If the dump fails, the server refuses to migrate and says why, so an upgrade never changes the schema without a copy.

A database migrated by a newer Stockroom, or by a different build, refuses to start one that lacks any of its migrations. Install the newer version again, or restore the pre-migrate dump into an empty database.

### PostgreSQL major versions

Nothing upgrades PostgreSQL's major version by surprise. Debian and Ubuntu keep a cluster on its version until someone runs `pg_upgradecluster`, and Homebrew's `postgresql@17` is pinned to 17. To move to a newer major version:

1. **Admin → Backup → Export everything**, and keep the zip somewhere off the machine.
2. Upgrade PostgreSQL the way your system does it: `pg_upgradecluster` on Debian and Ubuntu, or install the newer `postgresql@N` formula on macOS.
3. Run `stockroom setup` again. It creates the role and database if the new cluster doesn't have them.
4. Sign in with the failsafe admin and restore the export in **Admin → Backup → Restore**.

## Uninstalling

Linux and WSL:

```bash
sudo apt remove stockroom   # stops the service, keeps the config and all data
sudo apt purge stockroom    # also removes /etc/stockroom, setup's drop-in and a --config file
```

Neither touches the database or `/var/lib/stockroom`. To delete everything, after exporting anything you want to keep:

```bash
sudo -u postgres dropdb stockroom
sudo -u postgres dropuser stockroom
sudo rm -rf /var/lib/stockroom
```

Under WSL, also remove the `Stockroom WSL` task in Task Scheduler.

macOS:

```bash
stockroom service uninstall       # removes the Stockroom LaunchDaemon
brew uninstall stockroom
```

The PostgreSQL LaunchDaemon, the database and `$(brew --prefix)/var/stockroom` stay. To delete them too: `sudo launchctl bootout system/com.stockroom.postgresql`, `sudo rm /Library/LaunchDaemons/com.stockroom.postgresql.plist`, `dropdb stockroom`, `dropuser stockroom`, then `rm -rf "$(brew --prefix)/var/stockroom"`.

## Power loss and restarts

- The service starts at boot after PostgreSQL, whether or not anyone logs in, and restarts the server if it exits.
- PostgreSQL replays its write-ahead log on startup, so committed data survives an abrupt shutdown. Only transactions in progress at the moment of the cut are lost.
- If the last successful backup is older than a day, the server runs one soon after it starts.

## Troubleshooting

Start with doctor. It checks the config, PostgreSQL, the migrations, the service, the port, rclone, `pg_dump`, free disk and the age of the last backup, and gives a one-line fix for each problem:

```bash
stockroom doctor
```

Other commands:

```bash
curl http://127.0.0.1:8080/health   # server and database status
stockroom version                   # version, commit and schema
stockroom service status
```

Linux logs: `journalctl -u stockroom -n 100`. macOS logs: `$(brew --prefix)/var/log/stockroom.log`.

To ask for help, attach a support bundle to the issue:

```bash
sudo stockroom support-bundle      # Linux; on macOS, no sudo
```

It writes `stockroom-support-<date>.zip` in the current folder. The zip holds doctor's report, the version, the config and the last 2,000 lines of the service log. Passwords, tokens and the failsafe admin's number are removed, and no records from the database are in it: doctor's report says only the PostgreSQL version, whether the schema is current, the folders chosen in the admin panel and the age of the last backup. Read it before you send it.

If no admin can sign in and the failsafe admin isn't set, `stockroom restore` loads a backup archive from the command line with no session. `stockroom restore -h` lists its flags.

Running `stockroom setup` again repairs a broken install.

## Installing from a checkout

Until the packages have passed their tests on real machines, the older installer still works. It builds from a checkout, so it needs Go, Node and Docker, and it runs PostgreSQL in a `postgres:17` container under `~/Stockroom`:

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/install.sh
```

Its flags match setup's, plus `--home <path>`. On macOS its service is a LaunchAgent, which only starts at login because Docker Desktop only runs in a user session, so turn on **System Settings → Users & Groups → Automatic login**. `git pull` then `./scripts/install.sh` upgrades it, dumping the database first. This installer will be removed once the packages are proven.
