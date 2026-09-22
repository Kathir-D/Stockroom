/**
 * One function per endpoint in `CLAUDE.md` §8.1, in the order that table lists
 * them. Nothing here contains a rule: permission checks, the 7-day cap and the
 * overdue block all live in `internal/stockroom` and are enforced there
 * regardless of what this file sends (CLAUDE.md §7, §13). A wrapper that
 * "helpfully" pre-checks a rule would be a second definition of it.
 */

import { ApiError, request, setToken } from "./client";
import type {
  AssetDetail,
  AssetInput,
  AssetListItem,
  AssetStatus,
  BackupResult,
  BackupStatusResult,
  BackupVersion,
  Category,
  CategoryInput,
  CategoryNode,
  CheckInResult,
  CheckoutInput,
  CheckoutResult,
  CustodyRecord,
  DriveConnectResult,
  HealthResult,
  KitCheckInResult,
  KitDetail,
  KitInput,
  LoginResult,
  MeResult,
  PhotoMirrorStatus,
  PhotoWallStatus,
  Profile,
  RestoreResult,
  RosterResult,
  AssetImportResult,
  BulkAddInput,
  BulkPreview,
  CategoryImportResult,
  LabelLayout,
  ScanResult,
  SignInPhotos,
  Settings,
  SettingsInput,
  UserInput,
} from "./types";

export * from "./types";
export {
  ApiError,
  apiBaseUrl,
  configureApi,
  DEFAULT_BASE_URL,
  fileUrl,
  getToken,
  lastRequestAt,
  setToken,
} from "./client";

/**
 * How far ahead a due date may be. The server enforces it as an exact instant,
 * `now + 7 * 24h` at the moment of the request, not "the end of the seventh
 * day" (CLAUDE.md §13, 2026-09-12) — which is why `<DueDatePicker>` disables
 * dates past the cap instead of validating after the fact.
 */
export const MAX_CHECKOUT_DAYS = 7;

/* -------------------------------------------------------------- health ---- */

export function health() {
  return request<HealthResult>("/health", { anonymous: true });
}

/* ------------------------------------------------- sign-in photo wall ---- */

/**
 * A batch of tile URLs for the sign-in photo wall
 * (docs/design/signin-photo-wall.html §5).
 *
 * `anonymous` because there is no session at the sign-in screen and the route
 * requires none -- it is the one thing the server hands out to nobody in
 * particular. Always 200, so the only failure this can throw is the transport
 * one, which the component swallows: the wall is decoration and must not make
 * noise on a machine with no internet.
 */
export function signInPhotos() {
  return request<SignInPhotos>("/signin/photos", { anonymous: true });
}

/**
 * What the sign-in field filters, fetched before anybody has a session
 * (`lib/student-number.ts`). Returned raw: `ruleFrom` owns the fallback.
 */
export function signInConfig() {
  return request<unknown>("/signin/config", { anonymous: true });
}

/* ---------------------------------------------------------------- auth ---- */

/**
 * Scan login: no password. The frontend calls this when the keystroke burst
 * looked like a scanner (CLAUDE.md §10). `needs_password: true` means the token
 * is limited and only `setPassword`, `logout` and `me` will answer.
 */
export async function loginByScan(studentNumber: string) {
  const result = await request<LoginResult>("/auth/scan", {
    method: "POST",
    anonymous: true,
    body: { student_number: studentNumber },
  });
  setToken(result.token);
  return result;
}

/** Typed login: the same number entered by hand needs the password. */
export async function loginByPassword(studentNumber: string, password: string) {
  const result = await request<LoginResult>("/auth/password", {
    method: "POST",
    anonymous: true,
    body: { student_number: studentNumber, password },
  });
  setToken(result.token);
  return result;
}

/** Only valid while the account has no password. Upgrades the token in place. */
export function setInitialPassword(password: string) {
  return request<{ ok: boolean }>("/auth/set-password", {
    method: "POST",
    body: { password },
  });
}

export async function logout() {
  try {
    await request<{ ok: boolean }>("/auth/logout", { method: "POST" });
  } catch (error) {
    // An expired token cannot be logged out, and that is not a failure worth
    // showing anyone: the local session is being dropped either way.
    if (!(error instanceof ApiError && error.isUnauthorized)) throw error;
  } finally {
    setToken(null);
  }
}

export function me() {
  return request<MeResult>("/me");
}

/* -------------------------------------------------------------- browse ---- */

export function categoryTree() {
  return request<CategoryNode[]>("/categories/tree");
}

export interface AssetQuery {
  /** Any node; matches it and every descendant, so a Type shows every Model. */
  category?: string;
  status?: AssetStatus;
  /** Free text over name, description, serial number and asset tag. */
  q?: string;
  /** So the shape satisfies `request`'s query bag without a cast. */
  [key: string]: string | undefined;
}

export function listAssets(query: AssetQuery = {}, signal?: AbortSignal) {
  return request<AssetListItem[]>("/assets", { query, signal });
}

export function getAsset(id: string) {
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}`);
}

/* ----------------------------------------------------------- core loop ---- */

/**
 * The one endpoint behind every item barcode. A checked-out item is checked in
 * immediately; anything else comes back as the detail payload, byte-for-byte
 * what `getAsset` returns, so the frontend cannot tell a scan from a click
 * (CLAUDE.md §1.5, design-system.md §8.6).
 */
export function scan(serial: string) {
  return request<ScanResult>("/scan", { method: "POST", body: { serial } });
}

/** Called once per checkout. One transaction: the whole cart, or none of it. */
export function checkout(input: CheckoutInput) {
  return request<CheckoutResult>("/checkout", { method: "POST", body: input });
}

/** Any signed-in user may return any item. `note` is the damage note. */
export function checkIn(assetId: string, note?: string) {
  const trimmed = note?.trim();
  return request<CheckInResult>(
    `/assets/${encodeURIComponent(assetId)}/checkin`,
    {
      method: "POST",
      body: trimmed ? { note: trimmed } : {},
    },
  );
}

/**
 * Attach a damage note to a custody event that is already closed.
 *
 * The scan flow checks an item in the instant the barcode is read, with no
 * confirm press (CLAUDE.md §1.5), so the "Add a note" field on the confirmation
 * surface has no check-in call left to ride along with. The note lands on the
 * same `condition_in` column `checkIn` writes, so a return's condition has one
 * home however it was entered (design-system.md §8.6).
 */
export function annotateCustody(custodyEventId: string, note: string) {
  return request<CustodyRecord>(
    `/custody/${encodeURIComponent(custodyEventId)}/note`,
    {
      method: "POST",
      body: { note },
    },
  );
}

/* ----------------------------------------------------------------- kits ---- */

/**
 * Every kit with its units. Any signed-in user: a student has to be able to see
 * what is in a kit to decide to take it, the same rule that makes the current
 * holder of a unit visible to everyone (CLAUDE.md §7).
 */
export function listKits() {
  return request<KitDetail[]>("/kits");
}

export function getKit(id: string) {
  return request<KitDetail>(`/kits/${encodeURIComponent(id)}`);
}

/** Admin. A new kit starts empty; the units go in one at a time below. */
export function createKit(input: KitInput) {
  return request<KitDetail>("/kits", { method: "POST", body: input });
}

/** Admin. Renames a kit. Membership is untouched — see `addKitItem`. */
export function updateKit(id: string, input: KitInput) {
  return request<KitDetail>(`/kits/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: input,
  });
}

/**
 * Admin. Removes the grouping only: no asset, status or custody row moves, so
 * unlike `deleteAsset` this is allowed while the units are out.
 */
export function deleteKit(id: string) {
  return request<{ ok: boolean }>(`/kits/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

/**
 * Admin. 409 when the unit is already in a kit, and the message names which
 * one — an asset belongs to exactly one kit. Show it as written.
 */
export function addKitItem(kitId: string, assetId: string) {
  return request<KitDetail>(`/kits/${encodeURIComponent(kitId)}/items`, {
    method: "POST",
    body: { asset_id: assetId },
  });
}

export function removeKitItem(kitId: string, assetId: string) {
  return request<KitDetail>(
    `/kits/${encodeURIComponent(kitId)}/items/${encodeURIComponent(assetId)}`,
    { method: "DELETE" },
  );
}

/**
 * Returns every unit of the kit that is out, in one press. Any signed-in user,
 * like a single check-in. The response is per unit — `returned`, `already_in`
 * and `failed` — because a kit return is not all-or-nothing.
 */
export function checkInKit(kitId: string) {
  return request<KitCheckInResult>(
    `/kits/${encodeURIComponent(kitId)}/checkin`,
    { method: "POST" },
  );
}

/* ------------------------------------------------------------- custody ---- */

/** Admin. The roster of who has what — not the current holder of a named item. */
export function activeCustody() {
  return request<CustodyRecord[]>("/custody/active");
}

/** Admin. Sorted most-late first, which is what the overdue screen shows. */
export function overdueCustody() {
  return request<CustodyRecord[]>("/custody/overdue");
}

/** Admin. The full past-custodian trail for one asset. */
export function assetHistory(assetId: string) {
  return request<CustodyRecord[]>(
    `/assets/${encodeURIComponent(assetId)}/history`,
  );
}

/** Own history, or anyone's if the actor is an admin. */
export function userHistory(userId: string) {
  return request<CustodyRecord[]>(
    `/users/${encodeURIComponent(userId)}/history`,
  );
}

/* --------------------------------------------------------------- users ---- */

export function listUsers() {
  return request<Profile[]>("/users");
}

export function getUser(id: string) {
  return request<Profile>(`/users/${encodeURIComponent(id)}`);
}

/** The account starts with no password; it sets one at its first scan login. */
export function createUser(input: UserInput) {
  return request<Profile>("/users", { method: "POST", body: input });
}

export function updateUser(id: string, input: UserInput) {
  return request<Profile>(`/users/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: input,
  });
}

export function deleteUser(id: string) {
  return request<{ ok: boolean }>(`/users/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

/** Admin reset. The server drops that user's sessions itself. */
export function setUserPassword(id: string, password: string) {
  return request<{ ok: boolean }>(`/users/${encodeURIComponent(id)}/password`, {
    method: "POST",
    body: { password },
  });
}

/**
 * Roster CSV import. Relative `photo_path` values in the CSV resolve against
 * `photoDir`, which is why a roster with photos has to come in as multipart.
 */
export function importRoster(file: File, photoDir?: string) {
  const form = new FormData();
  form.set("file", file);
  if (photoDir) form.set("photo_dir", photoDir);
  return request<RosterResult>("/users/import", { method: "POST", form });
}

/* -------------------------------------------------------------- assets ---- */

/** POST /assets/import: a CSV upserted by serial, per-row results. */
export function importAssets(file: File) {
  const form = new FormData();
  form.set("file", file);
  return request<AssetImportResult>("/assets/import", { method: "POST", form });
}

/** The serials a bulk add would create. Writes nothing. */
export function bulkPreview(input: BulkAddInput) {
  return request<BulkPreview>("/assets/bulk-preview", { method: "POST", body: input });
}

export function bulkAdd(input: BulkAddInput) {
  return request<AssetDetail[]>("/assets/bulk", { method: "POST", body: input });
}

/* ------------------------------------------------------------ barcodes ---- */

export function labelLayouts() {
  return request<LabelLayout[]>("/labels/layouts");
}

/** A PDF sheet of labels, in the order given. */
export function assetLabels(input: { asset_ids: string[]; layout: string; start_at?: number }) {
  return request<Blob>("/assets/labels.pdf", { method: "POST", body: input, blob: true });
}

/** Printable ID cards carrying each student number as a barcode. */
export function userCards(userIds: string[]) {
  return request<Blob>("/users/cards.pdf", { method: "POST", body: { user_ids: userIds }, blob: true });
}

/**
 * One asset's barcode as a PNG. Fetched rather than put in an <img src>,
 * because an <img> cannot send the bearer token and the dev UI is on another
 * origin from the server, so the cookie does not travel either.
 */
export function assetBarcode(assetId: string) {
  return request<Blob>(`/assets/${encodeURIComponent(assetId)}/barcode.png`, {
    query: { width: "600" },
    blob: true,
  });
}

export function createAsset(input: AssetInput) {
  return request<AssetDetail>("/assets", { method: "POST", body: input });
}

export function updateAsset(id: string, input: AssetInput) {
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: input,
  });
}

/**
 * Refused with 409 for anything that has ever been checked out, because
 * `custody_events` cascades and the trail is the point. The message says to
 * mark it unavailable instead; show it as written.
 */
export function deleteAsset(id: string) {
  return request<{ ok: boolean }>(`/assets/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

/** The available/unavailable toggle. `checked_out` is not an accepted value. */
export function setAssetStatus(
  id: string,
  status: Extract<AssetStatus, "available" | "unavailable">,
) {
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}/status`, {
    method: "POST",
    body: { status },
  });
}

/** 10 MB cap, `.jpg .jpeg .png .gif .webp` only, enforced server-side. */
export function setAssetPhoto(id: string, photo: File) {
  const form = new FormData();
  form.set("photo", photo);
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}/photo`, {
    method: "POST",
    form,
  });
}

/* ---------------------------------------------------------- categories ---- */

/**
 * POST /categories/import: an indented outline (the examples/ Markdown files)
 * or a type,category,model CSV. All or nothing; a refusal names the line.
 */
export function importCategories(file: File) {
  const form = new FormData();
  form.set("file", file);
  return request<CategoryImportResult>("/categories/import", { method: "POST", form });
}

export function createCategory(input: CategoryInput) {
  return request<Category>("/categories", { method: "POST", body: input });
}

export function updateCategory(id: string, input: CategoryInput) {
  return request<Category>(`/categories/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: input,
  });
}

/** Refused when the node has children or assets. Say which. */
export function deleteCategory(id: string) {
  return request<{ ok: boolean }>(`/categories/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

/* --------------------------------------------------------------- admin ---- */

/**
 * Runs the whole backup: the archive into the configured folder, the photo
 * mirror, and a push to every enabled target. 503 when no folder is set, with
 * the message intact — that is a setting nobody filled in, not a bug.
 *
 * A push that fails does not fail the run: look at `targets` for which one.
 */
export function backupNow() {
  return request<BackupResult>("/admin/backup", { method: "POST" });
}

export function backupStatus() {
  return request<BackupStatusResult>("/admin/backup/status");
}

/** The restore-by-date picker. `local` reads the folder on this machine. */
export function backupVersions(target: "local" | "drive" | "github") {
  return request<BackupVersion[]>("/admin/backup/versions", {
    query: { target },
  });
}

/* ------------------------------------------------------------ settings ---- */

/** The secrets come back blank, with a `_set` boolean beside them. */
export function getSettings() {
  return request<Settings>("/admin/settings");
}

/** A partial update: an omitted field is left alone, an omitted secret kept. */
export function saveSettings(input: SettingsInput) {
  return request<Settings>("/admin/settings", { method: "PUT", body: input });
}

/**
 * Runs the target's own connection check. It exists so a misconfiguration is
 * found by somebody standing at the machine rather than by nothing happening
 * at 2 a.m., so show the error it returns as written.
 */
export function testBackupTarget(target: "drive" | "github") {
  return request<{ ok: boolean }>("/admin/settings/test", {
    method: "POST",
    body: { target },
  });
}

/** Starts `rclone authorize drive` and returns the link for the admin to open. */
export function connectDrive() {
  return request<DriveConnectResult>("/admin/drive/connect", {
    method: "POST",
  });
}

/**
 * Finishes the connection. `code` is the block rclone printed, needed only
 * when the browser callback did not reach it.
 */
export function finishDriveConnect(id: string, code: string, remote: string) {
  return request<Settings>("/admin/drive/finish", {
    method: "POST",
    body: { id, code, remote },
  });
}

/* ------------------------------------------------------------- restore ---- */

/**
 * Restoring replaces every record in the database, so it is the one action in
 * the app that asks for a word to be typed. The server checks it too — the
 * confirmation is a rule, not a UI courtesy.
 */
export const RESTORE_CONFIRMATION = "RESTORE";

export function restoreFromFile(
  file: File,
  options: { confirm: string; passphrase?: string; force?: boolean },
) {
  const form = new FormData();
  form.set("file", file);
  form.set("confirm", options.confirm);
  if (options.passphrase) form.set("passphrase", options.passphrase);
  if (options.force) form.set("force", "true");
  return request<RestoreResult>("/admin/restore", { method: "POST", form });
}

export function restoreFromTarget(
  target: "local" | "drive" | "github",
  id: string,
  options: { confirm: string; passphrase?: string; force?: boolean },
) {
  return request<RestoreResult>("/admin/restore/remote", {
    method: "POST",
    body: {
      target,
      id,
      confirm: options.confirm,
      passphrase: options.passphrase,
      force: options.force ?? false,
    },
  });
}

/* -------------------------------------------------------------- photos ---- */

export function photoGenerations() {
  return request<PhotoMirrorStatus>("/admin/photos/generations");
}

export function restorePhotos(generation: string) {
  return request<{ ok: boolean; restored: number }>("/admin/photos/restore", {
    method: "POST",
    body: { generation },
  });
}

/**
 * The only deletion the photo mirror has, and it is a person pressing a
 * button. Nothing is purged automatically: auto-purging the only copy of a
 * deleted photo defeats the mirror.
 */
export function deletePhotoGeneration(name: string) {
  return request<{ ok: boolean }>(
    `/admin/photos/generations/${encodeURIComponent(name)}`,
    {
      method: "DELETE",
    },
  );
}

/* ----------------------------------------------------------- photo wall ---- */

/**
 * The sign-in photo wall's Drive folder
 * (docs/design/signin-photo-wall.html §7).
 *
 * The status never carries the folder id or a Drive URL — the link is a
 * capability, so the field is write-only and the screen is given a label,
 * counts and a preview strip instead. Nothing in this file should ever be
 * changed to render one back.
 */
export function photoWallStatus() {
  return request<PhotoWallStatus>("/admin/photo-wall");
}

/**
 * Replace the folder. The server parses the link, *probes Drive with it* and
 * only then writes: a folder it cannot reach leaves the previous one live and
 * nothing is saved. Show the error it returns as written — it names the
 * accepted link forms, or says the folder is not shared with this machine's
 * Google account, which is what it almost always is.
 */
export function setPhotoWallFolder(link: string, label: string) {
  return request<PhotoWallStatus>("/admin/photo-wall", {
    method: "PUT",
    body: { link, label },
  });
}

/**
 * Re-list the folder now instead of waiting out the weekly refresh. The
 * listing runs in the background, so the useful thing to do with the response
 * is start polling `photoWallStatus()` for `listed_so_far`.
 */
export function rebuildPhotoWall() {
  return request<PhotoWallStatus>("/admin/photo-wall/rebuild", {
    method: "POST",
  });
}

/**
 * Up to six tiles from the reel **without** marking them served: a preview
 * must not consume the buffer the sign-in screen is about to draw from.
 *
 * The same tiles stay available to sign-in, which means a URL here can go
 * stale — treat a 404 the way the wall does, by hiding that tile.
 */
export function photoWallPreview() {
  return request<{ photos: string[] }>("/admin/photo-wall/preview");
}
