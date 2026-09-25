# Setting up backups

Stockroom backs up every item, account and checkout every night, and can push
the backup to Google Drive or GitHub. This page is the step-by-step setup. Apart
from the failsafe admin in Step 6, everything is done in the admin panel.

Everything here happens in **Admin → Settings**, except installing `rclone`,
which is one command and only needed for Google Drive.

Step 1 alone gives you a working local backup. Google Drive and GitHub keep a
copy off the machine, in case the machine itself is lost or damaged.

---

## Step 1: choose a backup folder

1. **Admin → Settings → Folders**
2. **Backup folder**: a full path on this machine, somewhere with room to grow.
   - Windows: `C:\stockroom-backups`
   - macOS: `/Users/<you>/stockroom-backups`
3. **Photo mirror folder** (optional): the same idea, for item and profile
   photos. Leave it empty to skip photos entirely.
4. **Save folders**

> **The path must be absolute**, starting with `/` on macOS and Linux or a drive
> letter such as `C:\` on Windows. Copying a path out of Finder or Explorer can
> drop the leading separator. Stockroom refuses relative paths.

Then **Admin → Backup → Back up now**. It should say how many rows it wrote and
where. If it does not, the message on screen says what is wrong.

> **Photos are only copied locally.** They are not pushed to Drive or GitHub,
> so a failed disk loses both the photos and their mirror. See
> `docs/design/backup.md` §H.

---

## Step 2: when it runs, and how long it is kept

**Admin → Settings → Schedule and retention**

| Field | What it does | Suggested |
|---|---|---|
| Run at (hour) | Hour of the day, 0–23, on this machine's clock | `2` (2 a.m.) |
| Keep backups for (days) | Older local folders are deleted | `90` |
| Warn after (hours) | How stale a backup gets before everyone signing in is told | `48` |

The backup runs **inside Stockroom itself**, so nothing has to be scheduled in
Windows or macOS. The machine only has to be switched on. If it was off at the
scheduled hour, the next start runs one straight away.

---

## Step 3: Google Drive (optional)

### 3a. Install rclone *(once, per machine)*

- **Windows**: open PowerShell and run `winget install Rclone.Rclone`.
  If that does not work, download the Windows zip from
  <https://rclone.org/downloads/>, extract it to `C:\rclone`, then
  Start → "Edit the system environment variables" → **Environment Variables** →
  select **Path** → **Edit** → **New** → `C:\rclone`.
- **macOS**: `brew install rclone`
- Check it worked: `rclone version` should print a version number.

If you skip this, Stockroom's **Connect** button will tell you rclone is
missing and repeat the command for your machine.

### 3b. Connect the Google account

1. **Admin → Settings → Google Drive → Connect**
2. A window appears with a sign-in link. **Open it** (or press **Copy** and
   paste it into a browser on this machine).
3. Sign in to the Google account the backups should live in.
4. Google will say **"Google hasn't verified this app."** This is expected for
   rclone. Click **Advanced**, then **Go to rclone (unsafe)**, then **Continue**.
5. Go back to Stockroom and press **Finish**. The sign-in normally completes on
   its own, so the paste box on that window is usually left empty; fill it in
   only if you ran rclone on another machine and it printed a block of text
   between two arrows.
6. Tick **Back up to Google Drive every night**, set **Folder in Drive** to
   something like `stockroom-backups`, and press **Save Drive settings**.
7. Press **Test connection**. It should say Google Drive is reachable.

> **Use a personal Google account, not the school one.** A Workspace
> administrator can block third-party apps for the whole domain, which revokes
> the connection without notice.

> **One connection, two features.** The sign-in photo wall reads its folder
> through this same Google connection, so connecting here turns the wall on as
> well, and **Sign in with Google** on the Photo wall screen connects backups
> too. There is only ever one Google sign-in to keep working.

### 3c. Your own Google client *(strongly recommended, about fifteen minutes)*

Out of the box, rclone signs in to Google with a "client" it shares with every
rclone user in the world. **rclone is retiring that shared client during 2026**
(<https://rclone.org/drive/#making-your-own-client-id>). When it stops, Drive
backups and the photo wall stop with it, and the backup screen says so. Until
you have your own, the backup screen shows a warning saying exactly that.

Any Google account can own the client; it does not have to be the one the
backups live in.

1. Open <https://console.cloud.google.com/>, and create a project (the name
   does not matter, "Stockroom" is fine).
2. **APIs & Services → Library**, search for **Google Drive API**, and press
   **Enable**.
3. **Google Auth Platform** (or **OAuth consent screen**) → **Get started**.
   App name `Stockroom`, your own email for both email fields, audience
   **External**, then **Create**.
4. **Data access → Add or remove scopes**, and under "Manually add scopes"
   paste `https://www.googleapis.com/auth/drive`, press **Add to table**, then
   **Update** and **Save**.
5. **Audience → Publish app**, and confirm. **This step matters.** An app left
   in *Testing* has its sign-ins expire after seven days, which on an unattended
   closet PC is a backup that dies a week after setup. Published but
   unverified is fine: you will see the "Google hasn't verified this app"
   screen when you sign in, exactly as before. (If **Publish app** is greyed
   out, Google wants a home page and privacy policy link under **Branding**
   first; any page you control will do.)
6. **Clients → Create client**, type **Desktop app**, then **Create**. Google
   shows a **Client ID** (ending in `.apps.googleusercontent.com`) and a
   **Client secret** (starting `GOCSPX-`).
7. In Stockroom: **Admin → Settings → Google sign-in**, paste both, press
   **Save Google client**.
8. **Google Drive → Reconnect**, and sign in again. That one sign-in moves both
   the backup and the photo wall onto your client; the warning goes away.

The secret is kept like the GitHub token: never shown again, never written into
a backup.

---

## Step 4: GitHub (optional, experimental)

GitHub needs no extra software on the machine.

> **This target is experimental.** It has not yet been tested against a real
> GitHub account. Use it as a second copy alongside Google Drive or the local
> folder, and test a restore from it (Step 7) before relying on it.

### 4a. Create the repository

1. Go to <https://github.com> and sign in (make an account if you have none).
2. **+** in the top right → **New repository**
3. Name it `stockroom-backup`
4. **Select Private.** The backup contains student numbers, which sign
   students in without a password.
5. Leave **Add a README file** unticked. Stockroom writes its own.
6. **Create repository**

### 4b. Create an access token

1. Your avatar (top right) → **Settings**
2. Very bottom of the left sidebar → **Developer settings**
3. **Personal access tokens** → **Fine-grained tokens** → **Generate new token**
4. Token name: `stockroom-backup`
5. Expiration: **No expiration**, or the longest your policy allows. When a
   token expires, backups to GitHub stop and the staleness warning appears.
6. **Repository access** → **Only select repositories** → `stockroom-backup`
7. **Permissions** → **Repository permissions** → **Contents** → change it to
   **Read and write**. Change nothing else.
8. **Generate token**, then **copy it**. GitHub shows it exactly once.

### 4c. Paste it in

1. **Admin → Settings → GitHub**
2. **Repository**: `your-username/stockroom-backup`
3. **Personal access token**: paste it
4. Tick **Back up to GitHub every night**, then **Save GitHub settings**
5. **Test connection**

Stockroom overwrites one `backup/` folder in the repository each night. Past
backups are kept as commits in the repository history.

---

## Step 5: encrypt the archive (optional)

**Admin → Settings → Archive encryption**

Off by default. With a passphrase set, the nightly archive is encrypted before
it leaves this machine, so a leaked Drive folder or repository is unreadable.

**Store the passphrase somewhere other than this machine.** It is not saved in
any backup target, and an archive cannot be restored without it.

Changing the passphrase does not re-encrypt archives already written. Those
still need the old one.

---

## Step 6: set a failsafe admin

This step edits `.env`. If the installer or the setup wizard already set a
failsafe admin, skip it.

The failsafe admin is an account Stockroom recreates from `.env` on every start.
It lets you sign in and restore a backup even when the database has no accounts.

1. Open `.env` in the Stockroom folder
2. Set `ADMIN_STUDENT_NUMBER` (in your student-number format) and
   `ADMIN_PASSWORD` (8 to 72 characters)
3. Restart Stockroom

**Admin → Backup** shows a warning while this is not set. Without it, restoring
into an empty database requires the command-line restore in Step 7.

---

## Step 7: getting the data back

There are four ways to restore.

1. **From a date.** Admin → Backup → **Pick a date** on the
   row for This machine, Google Drive or GitHub → choose a backup → type
   `RESTORE`. Nothing is downloaded by hand.
2. **From a file.** Admin → Backup → **Restore from a file** → choose a
   `stockroom-backup-*.zip`.
3. **Photos.** Admin → Backup → **Photo mirror** → **Copy back** on a
   generation.
4. **Stockroom will not start, or the database has no accounts in it.** Open a
   terminal on the closet PC and run:

   ```
   go run ./cmd/restore --yes path/to/backup-2026-09-16.zip
   ```

   This needs no sign-in. Every zip contains a `RESTORE.md` with these
   instructions.

**Restoring replaces every record in the database and signs everyone out.**
Checksums, row counts and foreign keys are verified before anything is
committed, so a failed restore leaves the database unchanged.

---

## When something goes wrong

If no backup has succeeded within `Warn after` hours, a warning appears at every
sign-in. Admins are told to open Admin → Backup, and students are told which
admin to contact.

**Admin → Backup** is where to look:

- **The banner at the top** lists current problems.
- **The target table** shows when each of This machine, Google Drive and GitHub
  last succeeded, and the exact error if the last attempt failed.
- **Recent runs** is the log, newest first.

A failed push does not fail the run. The local archive is still written, and
other targets are still tried.

---

## See also

- `docs/design/backup.md`: the full backup design
- `CLAUDE.md` §11: summary
- `RESTORE.md`: included in every backup zip
