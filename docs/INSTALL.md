# Installing Stockroom

This guide covers the production install on the machine that will run Stockroom day to day. For a development environment, see the [README](../README.md#development).

A production install runs one PostgreSQL container and one server binary. It does not use the Supabase CLI.

Supported platforms: macOS and Linux. Windows is not supported by the installer yet.

## What gets installed

```
~/Stockroom/
├── stockroom            server binary: API, web UI and database migrations
├── stockroom-run.sh     entry point the service runs
├── docker-compose.yml   one postgres:17 container, bound to 127.0.0.1
├── .env                 generated configuration, including the database password (mode 600)
├── uploads/             item and profile photos
├── backups/             nightly backup archives
├── photo-backups/       photo mirror
└── logs/server.log
```

The installer also registers a service that starts the server and restarts it if it exits. The nightly backup runs inside the server, so the service must be running for backups to happen.

## Install

Docker is required to run Stockroom. Go and Node are also required on the machine you install from, to build the binary.

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/install.sh
```

The installer:

1. checks that Docker is installed and running (on macOS it starts Docker Desktop and waits),
2. builds the web UI and compiles it into the server binary,
3. creates `~/Stockroom` and writes `.env` with a generated database password,
4. asks for a failsafe admin student number and password,
5. starts Postgres, applies migrations and starts the server,
6. registers the service and waits for `/health` to respond.

### Options

| Flag | Description |
|---|---|
| `--home <path>` | Install directory. Default `~/Stockroom` |
| `--addr <host:port>` | Server listen address. Default `127.0.0.1:8080` |
| `--no-service` | Do not register a service |
| `--no-open` | Do not open a browser when finished |
| `--admin-number <n>` | Failsafe admin student number, for unattended installs |
| `--admin-password-file <file>` | File containing the failsafe admin password. Use `-` to read from stdin |

There is no `--admin-password` flag, because command-line arguments are visible to other users and saved in shell history.

### Failsafe admin

The failsafe admin is recreated with admin rights and the configured password on every server start. It is the recovery account for a forgotten password or an empty database after a restore. It is optional. If it is not set, admins see a warning at sign-in.

### macOS: automatic login

On macOS the service is a LaunchAgent, which starts when the user logs in. Docker Desktop only runs inside a user session, so a boot-time daemon cannot be used.

Enable **System Settings → Users & Groups → Automatic login**. Without it, the machine stays at the login window after a restart and Stockroom does not run.

On Linux the service is a systemd unit that starts at boot.

## First run

Open `http://127.0.0.1:8080`, or the address the installer printed. The database starts empty, with no categories, equipment or accounts other than the failsafe admin.

The setup wizard opens automatically:

- If no failsafe admin was set, it asks you to create the first admin account.
- If a failsafe admin was set, sign in with it and the wizard opens.

The wizard covers the student-number format, lending rules, categories, equipment, the student roster and backups. Progress is saved, so you can leave and come back. It can be reopened from **Admin → Settings → Run the setup guide again**.

The [`examples/`](../examples/) folder contains sample files for each import. **Admin → Assets → Print labels** and **Admin → Users → Print ID cards** produce PDFs to print at 100% scale.

### Setting up without the wizard

Every wizard step is also available on the admin screens:

- Backups to `~/Stockroom/backups` and the photo mirror at `~/Stockroom/photo-backups` are configured by the installer. Google Drive and GitHub are set up in Admin → Settings ([`BACKUP-SETUP.md`](BACKUP-SETUP.md)).
- Categories, equipment and students are imported from Admin → Categories → Import, Admin → Assets → Import CSV and Admin → Users → Import roster. See [`ADMIN-GUIDE.md`](ADMIN-GUIDE.md).
- **Stop showing this guide**, at the bottom of each wizard page, marks setup as complete.

### Editing `.env`

After editing `~/Stockroom/.env`, restart the service.

| Value | Editable |
|---|---|
| `ADMIN_STUDENT_NUMBER`, `ADMIN_PASSWORD` | Yes. Applied on every start |
| `SESSION_IDLE_MINUTES` | Yes |
| `SERVER_ADDR` | Yes. The browser address changes to match |
| `BACKUP_DIR`, `PHOTO_BACKUP_DIR`, `RCLONE_REMOTE`, `SIGNIN_PHOTOS_FOLDER_ID` | No. Read on the first start only, then managed in the admin panel |
| `DATABASE_URL`, `POSTGRES_PASSWORD` | No. Must match the existing database container |

## Upgrading

```bash
cd Stockroom
git pull
./scripts/install.sh
```

The installer detects the existing install, dumps the database, rebuilds, replaces the binary and restarts the service. The server applies new migrations at startup. `.env` is not overwritten.

If the database container is stopped, the installer starts it before taking the dump. If the dump fails, the upgrade stops.

## Power loss and restarts

- The database container uses `restart: unless-stopped`, so Docker restarts it when the machine comes back.
- The service starts the server, which waits up to two minutes for the database.
- PostgreSQL replays its write-ahead log on startup, so committed data survives an abrupt shutdown. Only transactions in progress at the moment of the shutdown are lost.

## Troubleshooting

```bash
tail -50 ~/Stockroom/logs/server.log     # recent server output
curl http://127.0.0.1:8080/health        # server and database status
cd ~/Stockroom && docker compose ps      # database container status
```

macOS:

```bash
launchctl print gui/$(id -u)/com.stockroom.server       # service status
launchctl kickstart -k gui/$(id -u)/com.stockroom.server # restart
```

Linux:

```bash
systemctl status stockroom.service
sudo systemctl restart stockroom.service
```

Running `./scripts/install.sh` again also repairs a broken install.
