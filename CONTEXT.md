# Stockroom domain glossary

The words the code, the docs and the issues use, with the meaning fixed. When two words could name the same thing, the one here wins. CLAUDE.md is the architecture reference; this file only defines terms.

## People

**Account / profile.** A row in `profiles`. Two kinds, decided by `is_admin`: **admin** and **student** (the code says non-admin). Nothing else about a person changes what they may do; the `role` column is unused.

**Student number.** The digits on an ID card's barcode, `profiles.student_number`. The scan-login key and therefore a credential: a browse list never carries another person's number to a student.

**Actor.** The signed-in account behind one request, resolved from the session token on every call. Every package function that decides something takes an `Actor`.

**Session.** A token in the in-memory store. **Full** once the account has a password; **limited** after a scan login by an account without one, and then it may only set a password, sign out, or ask who it is.

**Failsafe admin.** The account named in `.env`, re-created with its password on every server start. The way back in when every UI path is broken.

## Things

**Asset / unit.** One physical object, a row in `assets`. "Unit" is used when the point is that two identical lenses are two rows.

**Serial.** `assets.serial_number`, unique, printed on the sticker. The scan key for items. Linear stock uses model-prefixed serials (`T7iBat-001`).

**Asset tag.** `assets.asset_tag`, unique, the human label. Not a scan key.

**Status.** `available`, `checked_out` or `unavailable` (the catch-all for broken, missing, retired). The enum has other labels; v1 never writes them. **Unavailable** is the only status an admin sets by hand.

**Category tree.** `categories`, a three-level tree via `parent_id`: **Type** (Lenses) → **Category** (Zooms) → **Model** (Canon 70-200mm f/2.8). A node's position among its siblings is `sort_order`, which follows `Catagories.md`'s document order. An asset may file under any node (ADR 0001), though the seed puts every unit under a Model.

**Kit.** A named bundle of units that goes out and comes back together — `kits` plus `kit_items` — "Kit #1 = this camera, this lens, this bag". A kit is a *label over assets* and never holds custody itself: adding one to the cart expands it into its asset ids, and checkout commits them like any other cart, one custody event per asset. An asset belongs to **at most one kit**, so a shared unit cannot make a second kit quietly incomplete. A kit is added to a cart whole or not at all; it is returned unit by unit, because the units come back to the counter one at a time.

**Photo.** A file under `UPLOADS_DIR`, either `profiles/<student number>.<ext>` or `assets/<asset id>.<ext>`. The row stores the relative path (`photo_path`); the API hands out the URL (`photo_url`, under `/files/`). Each table's `photo_path` has exactly one writer: `SetAssetPhoto` for assets, the roster import for profiles.

## Custody

**Custody event.** One row in `custody_events`: who took which asset, when, due when, and (once returned) who brought it back and in what condition. The full set for an asset or a person is its **history** or **trail**.

**Open custody / out.** A custody event with no `checked_in_at`. This row, not the status column, is what decides whether an item is out. The two agree in everything the app writes; when they drift, the row wins and the drift becomes visible.

**Custodian.** The person holding an item, `custody_events.custodian_id`. Distinct from **checked out by**, the actor who ran the checkout, which differs only when an admin checks out on someone's behalf.

**Current holder.** The custodian of an item's open custody event. Visible by name to every signed-in user; by student number to admins only.

**Cart.** The frontend's pending set of asset ids. Never sent to the server until **checkout**, which commits the whole cart in one transaction or none of it. A cart is a set: the same id twice is one item. It only ever holds items being borrowed; returns never touch it.

**Due date / due at.** The instant a checkout must be returned by, chosen by the user, at most seven days (7 × 24 h, exact) after the request.

**Overdue.** An open custody event whose `due_at` has passed. Defined once, by the `overdue_custody` view; the sign-in warning, the checkout block and the admin list all read it. An **overdue block** refuses a checkout to a custodian with anything overdue; an admin may **override** it per checkout.

**Check-in / return.** Closing an open custody event. Any signed-in user may return any item. An optional **damage note** lands on the event's `condition_in`.

**Scan.** A barcode arriving as a keystroke burst. Two decisions, made in two places. The frontend decides what the code *is* from which screen is active: on the sign-in screen it is a student number and goes to the login endpoint; anywhere else it is an asset serial and goes to `POST /scan`. The server then decides what a serial scan *does* from the open custody row: an item that is out is checked in, an item on the shelf comes back as the detail popup.

## Operations

**Roster import.** Upserting accounts from a CSV by student number. Names are replaced; `is_admin`, the password and (unless a new one is named) the photo survive.

**Backup.** One CSV per table into `BACKUP_DIR/<yyyy-mm-dd>/`, written whole to a side folder and moved into place. "Backup Now" in the admin panel and the nightly CLI run the same export; the CLI then hands the folder to `rclone`.

**Not configured.** A setting the operator left blank (`UPLOADS_DIR`, `BACKUP_DIR`). Answered with 503 and the message intact, because the admin reading it is the person who edits `.env`.

## Words avoided

**Booking / reservation.** Tables exist; nothing uses them. Say checkout.
**Location.** Same. Location is who has it.
**Role.** The enum column is unused. Say admin or student.
**Borrow / loan.** Say checkout.
