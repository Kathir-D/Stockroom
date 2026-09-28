# Questions people ask

Short answers, with a link where there's more. Students have a one-page guide, [`STUDENT-GUIDE.md`](STUDENT-GUIDE.md). Admins have [`ADMIN-GUIDE.md`](ADMIN-GUIDE.md).

## Borrowing

**Do I need a password?**
Not when you scan your ID card. Typing your number instead asks for your password, and so does any admin account, even after a scan. The first time you scan, Stockroom asks you to choose a password for the times you don't have your card.

**I forgot my password.**
Ask an admin to set a new one in **Admin → Users**, and your card still signs you in meanwhile.

**It says my number is locked.**
Five wrong passwords lock a number for five minutes. Wait, or scan your card.

**How long can I keep something?**
You pick the last day you need it, up to seven days from today unless your school changed that. It's due back at closing time on the next school day after that day, so bringing it back first thing the next morning is never late. Weekends and the days your school is closed don't count as school days.

**Why can't I check anything out?**
You have something overdue. Bring it back first. An admin can let a checkout through anyway, and a school can turn this rule off.

**Can I return something a friend borrowed?**
Yes. Scanning an item that's out returns it, whoever has it.

**I scanned something I just borrowed and it asked "Return it?"**
Scanning an item you checked out in the last ten minutes asks first, in case you scanned it by accident. Say yes if you meant to return it.

**It's broken.**
Return it and add a damage note. You can add one up to 30 minutes after the return. An admin sees it under **Needs attention**.

**I typed the serial because the sticker is gone. Is that all right?**
Yes. A return with no scan behind it goes to an admin to check, and the item is available again at once.

**It signed me out.**
Checking out signs you out after ten seconds unless you press **Keep going**, and ten minutes with nobody touching the machine does the same, so the next person never uses your account. Your cart is kept if you reload the page, and cleared when you sign out.

**Who has the camera I want?**
Open it. Anyone signed in sees who has it and when it's due back.

## Running it

**Does it need the internet?**
No. Only the nightly backup to Google Drive or GitHub does, and a failed push doesn't stop anything else.

**Can students use it from their phones or another computer?**
No. It runs on one computer and answers only that computer. Other machines on the network can't reach it. That's on purpose, because a scan of a student number signs that student in.

**Does it send overdue reminders by email?**
No. Overdue loans show in the app: to the student at sign-in, and to admins under **Admin → Overdue**.

**Can students reserve equipment?**
No. The first person to check an item out has it.

**The school is closed next week. Will loans fall due during the break?**
Not if you add the dates in **Admin → Settings → Loans → Days the school is closed**. One date or range per line, such as `2026-12-21 to 2027-01-01`. A loan that would fall due on a closed day falls due on the first day back.

**Why can't admins see a student number next to a name?**
Everyone can see who has an item. Only admins see the holder's student number, because a scan of that number signs its owner in.

**Why won't it accept a serial number?**
A serial can't equal anybody's student number, ignoring upper and lower case, because a scanned code has to be either an item or a card, never both. Serials must also be unique.

**A student graduated. Do I delete them?**
Archive them in **Admin → Users**. They can't sign in and drop out of the lists, but their history stays. An account that ever borrowed anything can't be deleted.

**An item is lost.**
Find its loan in **Admin → Overdue**, which also lists everything out, and press **Mark lost**. The loan closes as lost, the item becomes unavailable, and the student stops being overdue on it.

**Everyone got signed out at once.**
The server restarted. Sessions live in memory, so a restart signs everyone out, and nothing else is lost.

**An admin forgot their password, and there's no other admin.**
Use the failsafe admin: set `ADMIN_STUDENT_NUMBER` and `ADMIN_PASSWORD` in the config file and restart the service. Every start makes sure that account exists and has that password. [`INSTALL.md`](INSTALL.md) says where the config file is.

**The computer died.**
Install Stockroom on another one and restore last night's backup: **Admin → Backup → Restore** once you can sign in, or `stockroom restore` when there's no account to sign in with. [`BACKUP-SETUP.md`](BACKUP-SETUP.md) walks through it.

**Something is wrong and I don't know what.**
Run `stockroom doctor`. It checks the install and says how to fix each problem. To ask for help, run `stockroom support-bundle` (with `sudo` on Linux) and attach the zip it writes to an issue. It has no student data in it, and passwords and tokens are removed.

**What should I buy?**
[`HARDWARE.md`](HARDWARE.md): a USB barcode scanner that types like a keyboard, and matte label sheets.
