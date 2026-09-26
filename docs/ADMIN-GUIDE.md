# Admin guide

This is for whoever looks after Stockroom day to day. You do not need to be
technical, and nothing here asks you to edit a file. Installing it is
[`INSTALL.md`](INSTALL.md), and backups have their own walkthrough,
[`BACKUP-SETUP.md`](BACKUP-SETUP.md). Students get
[`STUDENT-GUIDE.md`](STUDENT-GUIDE.md), one page, to print and tape up.

Everything below is in the **Admin** section of the sidebar, which only admin
accounts see.

---

## Building the category tree

**Admin → Categories.** Stockroom files equipment in three levels:

```
Type              Lenses
  Category          Zooms
    Model             Canon 70-200mm f/2.8
```

Students browse down this tree, so build it the way they think about the
shelf. Two things catch everybody once:

- **Names are unique across the whole tree**, not just under one parent. You
  cannot have "Accessories" under both Lenses and Audio. Call them "Lens
  Accessories" and "Audio Accessories".
- **The order is yours.** Types and categories show in the order you set, not
  alphabetically.

Typing a large tree by hand is slow. **Import** takes a whole tree at once, as
a Markdown outline or a `type,category,model` spreadsheet.
[`examples/categories.media-department.md`](../examples/categories.media-department.md)
shows the shape. Importing a corrected file again only adds what is new, so it
is safe to fix a typo and re-run it.

## Adding equipment

**Admin → Assets.** Every physical thing is its own row. Two identical cameras
are two rows, because each one needs its own sticker.

| Button | Use it for |
|---|---|
| **New asset** | One item. Give it a serial number, a name and a category |
| **Add several** | A batch of identical things: 20 batteries become `T7B-001` to `T7B-020`. It shows you the serials before it creates anything |
| **Import CSV** | A spreadsheet of everything at once. [`examples/assets.csv`](../examples/assets.csv) shows the columns |

The **serial number** is what the sticker encodes and what the scanner reads.
It has to be unique, and it should be short: the small labels for batteries and
SD cards fit about 9 characters on US Letter paper and 7 on A4. If an item
already has a manufacturer's barcode that is unique to it, you can use that as
the serial and skip the sticker.

Importing a spreadsheet with a serial that already exists **updates** that
item, and the report says what it changed.

**Broken, lost or retired?** Press **Mark unavailable** on its row. It stays in
the records, with its whole history, and nobody can borrow it. Deleting is
only possible for an item that has never been checked out, so its history is
kept.

## Printing stickers

**Admin → Assets → Print labels** makes a PDF of barcode stickers for whatever
the list is showing. Pick the label size by what it goes on, and **print at
100%**, never "fit to page", or the bars come out the wrong size.

- Matte labels, not glossy. Glossy ones reflect the lamp and will not scan.
- Put them where hands do not rub: the underside of a camera, the end of a
  lens barrel, the top of a battery.
- If you already used some labels on a sheet, tick **Skip labels already used
  on the first sheet** and it starts further down.

A sticker that falls off: open the item and press **Barcode**. It shows one
big enough to scan straight off the screen until you print a new one.

## Adding students

**Admin → Users → Import roster** takes a class list as a CSV with these
columns:

```csv
first_name,last_name,student_number
```

Most school systems can export something close. Open it in a spreadsheet,
rename the columns to match, delete the rest, and save as CSV.
[`examples/roster.csv`](../examples/roster.csv) is a working example.

- **Re-importing next term's list is safe.** Students are matched by number.
  Names get updated, and nobody loses their password or admin status.
- **New students have no password.** The first time they scan their card, it
  asks them to choose one. That takes about ten seconds and needs nothing from
  you.
- **One student?** **New user**, then fill in the form.

If your students' ID cards have no barcode, **Print ID cards** makes a sheet of
cards that do, for everyone the list is showing. Search first to print a few.

**Forgotten password?** The key icon on the student's row is **Set password**.
It signs them out everywhere. Remind them that scanning the card never needs
the password anyway.

**Making someone an admin:** edit their row and tick **Administrator**. That is
the only permission there is. An admin can do everything.

## The overdue list

**Admin → Overdue** lists everything past its due date, the most overdue first, with
who has it and how many days late it is.

Students with an overdue item are warned when they sign in and **cannot borrow
anything else** until it comes back. If you need to lend to them anyway, check
the cart out yourself: an admin gets an **Override the overdue block** option
on the cart page.

## The activity log and the closet camera

**Admin → Activity** lists everything that happened at the closet, newest first: every sign-in (and failed sign-in), every barcode scan, every checkout and return, every change an admin made and, if the closet camera is on, every visit. Filter by time, person, type or text, and press **Export CSV** to take a range away. Nobody can edit or delete a row, not even an admin, and restoring a backup keeps everything logged since that backup.

With the camera on, each person who walks in gets a row with a snapshot and a **Play** button. Walking out adds a row that says how long they were inside. Recordings stay on this machine for the number of days set in **Settings → Closet camera** (30 by default) and are then deleted. Press **Keep this recording** in the player to keep one for good. Watching a recording is itself logged.

**When something goes missing**, open the item (Assets, or scan it), and under its custody history press **Closet activity since its last return**. The timeline opens at the moment it was last checked in, with every visit, sign-in and scan since. Stockroom does not match faces to accounts. You compare who walked in with who signed in.

If the camera stops working, admins see a notice at sign-in and the timeline gets a "Closet camera offline" row, then "back online after …" when it returns. Borrowing and returning never wait on the camera.

## When a student leaves with a camera

The item stays checked out to them, appears on the Overdue list, and the
history records who had it and when.

1. Chase it the usual way. The Overdue list gives you the name and the date.
2. If it comes back, scan it. Done.
3. If it is gone for good, press **Check in** on its row in **Overdue**, then
   **Mark unavailable** on it in **Assets**. That clears it off the list
   without pretending it is on the shelf, and the history still shows who had
   it last.

Stockroom does not allow deleting an account that still holds items.

## Handing Stockroom to next year's teacher

1. **Make them an admin** (Users → edit → Administrator).
2. **Give them the safety-net account.** That is the spare admin number and
   password from setup, the way back in if every other admin is locked out.
   **Admin → Settings → Run the setup guide again** has a step to replace it
   with one of theirs. Replacing it retires the old one.
3. **Hand over the backup keys.** Which Google account the Drive backup uses,
   and the archive passphrase if you turned encryption on. Without the
   passphrase, an encrypted backup cannot be opened by anybody, ever.
4. **Run a backup** (Admin → Backup → Back up now) and check it says it worked.
5. **Take your own admin off**, or ask them to.

---

## Getting everything out

**Admin → Backup → Export everything** downloads one zip of every record in
Stockroom to this computer. Use it if you want to stop using Stockroom, move
it to another computer, or just look at the data in Excel. It works even if
backups were never set up.

The zip is not encrypted, and it lists every student number beside their
password hash. A student number signs its owner in with a scan, so treat the
file like the class list it contains.

**Moving to another computer:** install Stockroom there, sign in as the
safety-net admin, and upload the zip under **Admin → Backup → Restore from a
file**. Every record comes back, passwords included. Two things do not travel
in the zip. Photos live in the `uploads` folder on the old computer, so copy
that folder across by hand. And install the same version of Stockroom on both.
A restore refuses a zip from a different version and says so. It has an
override for emergencies, but the same version on both is the safe path.

### The two files you will actually open

**`inventory.csv`**, one row per item:

| Column | What it means |
|---|---|
| `name` | What the item is called |
| `serial_number` | The code on its sticker |
| `asset_tag` | An internal number Stockroom made up (`AST-000123`). Ignore it |
| `type`, `category`, `model` | Where it sits in the category tree |
| `status` | `available`, `checked_out` or `unavailable` |
| `condition` | Free-text condition, if anyone wrote one |
| `held_by` | Who has it right now, if it is out |
| `student_number` | That person's number |
| `checked_out_at`, `due_at` | When it went out and when it is due back |
| `overdue` | `true` if it is past due |
| `photo_path` | Where its photo is on the Stockroom computer, if it has one |

**`accounts.csv`**, one row per person:

| Column | What it means |
|---|---|
| `student_number` | Their ID number |
| `first_name`, `last_name`, `full_name`, `email` | Their name, and email if you ever entered one |
| `is_admin` | `true` for admins |
| `password_hash` | Their password, scrambled one way. Nobody can read the password back out of it, but keep the file private anyway |
| `has_password` | `false` for students who have not scanned in for the first time yet |
| `created_at` | When the account was made |
| `photo_path` | Where their photo is, if they have one |

### Everything else in the zip

The `tables/` folder holds every database table as its own CSV, exactly as
stored. It is what a restore reads, and it is there so a developer could load
your data into something else. `manifest.json`, `sequences.csv` and
`RESTORE.md` let Stockroom check the zip is complete before restoring it.
`RESTORE.md` explains how to restore by hand.
