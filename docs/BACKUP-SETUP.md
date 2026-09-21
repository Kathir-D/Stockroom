# Setting up backups

Stockroom keeps a copy of everything — every item, every account, every
checkout — and can push it somewhere off the machine every night. This page is
the click-by-click setup, written for whoever is standing at the closet PC. You
do not need to be technical, and you will not have to edit a file.

Everything here happens in **Admin → Settings**, except installing `rclone`,
which is one command and only needed for Google Drive.

**You can stop after Step 1.** A local backup folder alone is a real backup and
is better than nothing. Google Drive and GitHub are what protect you if the
machine itself is lost or stolen, which is the case a local folder cannot help
with.

---

## Step 1 — choose a backup folder

1. **Admin → Settings → Folders**
2. **Backup folder**: a full path on this machine, somewhere with room to grow.
   - Windows: `C:\stockroom-backups`
   - macOS: `/Users/<you>/stockroom-backups`
3. **Photo mirror folder** (optional): the same idea, for item and profile
   photos. Leave it empty to skip photos entirely.
4. **Save folders**

> **It has to start with `/` (or `C:\` on Windows).** Copying a path out of a
> Finder or Explorer window often loses the leading separator, and
> `Users/you/Desktop/backups` is not the same place as `/Users/you/Desktop/backups`
> — the second is your Desktop, the first is a new folder created wherever
> Stockroom happens to be running from. Stockroom refuses a path without it
> rather than write a perfectly good backup somewhere you will never look.

Then **Admin → Backup → Back up now**. It should say how many rows it wrote and
where. If it does not, the message on screen says what is wrong.

> **Photos are only ever copied locally.** They are never pushed to Drive or
> GitHub. If the drive dies, the photos and their mirror die with it. That was a
> deliberate trade — see `docs/design/backup.md` §H.

---

## Step 2 — when it runs, and how long it is kept

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

## Step 3 — Google Drive (optional)

Drive is the easier of the two off-site options to hand over to somebody else
later: it is a Google sign-in screen they already recognise.

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
4. Google will say **"Google hasn't verified this app."** That is expected —
   rclone is open-source software and was never submitted to Google's review.
   Click **Advanced**, then **Go to rclone (unsafe)**, then **Continue**.
5. Go back to Stockroom and press **Finish**. The sign-in normally completes on
   its own, so the paste box on that window is usually left empty; fill it in
   only if Google showed you a block of text starting with `{`.
6. Tick **Back up to Google Drive every night**, set **Folder in Drive** to
   something like `stockroom-backups`, and press **Save Drive settings**.
7. Press **Test connection**. It should say Google Drive is reachable.

> **Use a personal Google account, not the school one.** A school Workspace
> administrator can block third-party apps across the whole domain. That would
> revoke the connection silently, and the first sign anyone would get is
> Stockroom's staleness warning two days later.

---

## Step 4 — GitHub (optional)

GitHub needs nothing installed, which is why it is worth having as well: it is
the harder of the two for a school firewall to block.

### 4a. Create the repository

1. Go to <https://github.com> and sign in (make an account if you have none).
2. **+** in the top right → **New repository**
3. Name it `stockroom-backup`
4. **Select Private.** This is not optional. The backup contains student
   numbers, and a student number signs its owner into Stockroom with no
   password — a public repository would publish a list of working logins.
5. Leave **Add a README file** unticked. Stockroom writes its own.
6. **Create repository**

### 4b. Create an access token

1. Your avatar (top right) → **Settings**
2. Very bottom of the left sidebar → **Developer settings**
3. **Personal access tokens** → **Fine-grained tokens** → **Generate new token**
4. Token name: `stockroom-backup`
5. Expiration: **No expiration**. A token that expires stops backups on a date
   nobody wrote down. If school policy forbids that, pick the longest allowed —
   the staleness warning will catch it within `Warn after` hours.
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

Stockroom overwrites one `backup/` folder in that repository every night rather
than adding a new one, so the repository stays small and GitHub's own history
becomes the list of past backups.

---

## Step 5 — encrypt the archive (optional)

**Admin → Settings → Archive encryption**

Off by default. With a passphrase set, the nightly archive is encrypted before
it leaves this machine, so a leaked Drive folder or repository is unreadable.

**If you turn this on, write the passphrase down somewhere that is not this
machine.** It is not stored in either backup target, and nobody — including us —
can recover an archive whose passphrase is lost. That is the trade: it swaps a
confidentiality risk for the risk that a lost passphrase means no backup at all,
which is why it is off unless you choose it.

Changing the passphrase does not re-encrypt archives already written. Those
still need the old one.

---

## Step 6 — set a failsafe admin

This one *is* a file, and it is the only one. It is also the most important
step on this page.

If the database is ever lost, the restore is done by signing in as an admin —
but the accounts live in the database that was lost. The failsafe admin is an
account Stockroom recreates from `.env` every time it starts, so there is always
somebody to sign in as.

1. Open `.env` in the Stockroom folder
2. Set `ADMIN_STUDENT_NUMBER` (digits only) and `ADMIN_PASSWORD` (at least 8
   characters)
3. Restart Stockroom

The **Admin → Backup** screen warns while this is not set. Without it, a wiped
database leaves the admin panel unreachable at exactly the moment you need it —
and the only way back in is the command-line restore in Step 7.

---

## Step 7 — getting the data back

There are four routes, and the first three need no typing beyond one word.

1. **From a date** *(the normal one)*. Admin → Backup → **Pick a date** on the
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

   Every zip contains a `RESTORE.md` with this written out. This route needs no
   sign-in, which is exactly why it exists: it is the one that still works when
   routes 1–3 cannot be reached.

**Restoring replaces every record in the database and signs everyone out.** It
is checked first — the archive's own checksums, then row counts, then every
foreign key, all before anything is committed — so a restore that fails leaves
the database exactly as it was.

---

## When something goes wrong

Stockroom does not fail quietly. A backup that has not run in `Warn after` hours
puts a line on **everybody's** screen when they sign in: admins are told to open
Admin → Backup, students are told which admin to mention it to.

**Admin → Backup** is where to look:

- **The banner at the top** lists everything currently wrong, in sentences.
- **The target table** shows when each of This machine, Google Drive and GitHub
  last succeeded, and the exact error if the last attempt failed.
- **Recent runs** is the log, newest first.

A push that fails does not fail the whole run. The restorable archive is already
on this machine, and one target being unreachable says nothing about the other —
that is the point of having two.

---

## See also

- `docs/design/backup.md` — the full design, the reasoning, and the known limits
- `CLAUDE.md` §11 — the short version
- `RESTORE.md` — inside every backup zip, for when nothing else is available
