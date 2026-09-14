/**
 * One function per endpoint in `CLAUDE.md` §8.1, in the order that table lists
 * them. Nothing here contains a rule: permission checks, the 7-day cap and the
 * overdue block all live in `internal/stockroom` and are enforced there
 * regardless of what this file sends (CLAUDE.md §7, §13). A wrapper that
 * "helpfully" pre-checks a rule would be a second definition of it.
 */

import { ApiError, request, setToken } from "./client"
import type {
  AssetDetail,
  AssetInput,
  AssetListItem,
  AssetStatus,
  BackupResult,
  Category,
  CategoryInput,
  CategoryNode,
  CheckInResult,
  CheckoutInput,
  CheckoutResult,
  CustodyRecord,
  HealthResult,
  LoginResult,
  MeResult,
  Profile,
  RosterResult,
  ScanResult,
  UserInput,
} from "./types"

export * from "./types"
export {
  ApiError,
  apiBaseUrl,
  configureApi,
  DEFAULT_BASE_URL,
  fileUrl,
  getToken,
  setToken,
} from "./client"

/**
 * How far ahead a due date may be. The server enforces it as an exact instant,
 * `now + 7 * 24h` at the moment of the request, not "the end of the seventh
 * day" (CLAUDE.md §13, 2026-09-12) — which is why `<DueDatePicker>` disables
 * dates past the cap instead of validating after the fact.
 */
export const MAX_CHECKOUT_DAYS = 7

/* -------------------------------------------------------------- health ---- */

export function health() {
  return request<HealthResult>("/health", { anonymous: true })
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
  })
  setToken(result.token)
  return result
}

/** Typed login: the same number entered by hand needs the password. */
export async function loginByPassword(studentNumber: string, password: string) {
  const result = await request<LoginResult>("/auth/password", {
    method: "POST",
    anonymous: true,
    body: { student_number: studentNumber, password },
  })
  setToken(result.token)
  return result
}

/** Only valid while the account has no password. Upgrades the token in place. */
export function setInitialPassword(password: string) {
  return request<{ ok: boolean }>("/auth/set-password", {
    method: "POST",
    body: { password },
  })
}

export async function logout() {
  try {
    await request<{ ok: boolean }>("/auth/logout", { method: "POST" })
  } catch (error) {
    // An expired token cannot be logged out, and that is not a failure worth
    // showing anyone: the local session is being dropped either way.
    if (!(error instanceof ApiError && error.isUnauthorized)) throw error
  } finally {
    setToken(null)
  }
}

export function me() {
  return request<MeResult>("/me")
}

/* -------------------------------------------------------------- browse ---- */

export function categoryTree() {
  return request<CategoryNode[]>("/categories/tree")
}

export interface AssetQuery {
  /** Any node; matches it and every descendant, so a Type shows every Model. */
  category?: string
  status?: AssetStatus
  /** Free text over name, description, serial number and asset tag. */
  q?: string
}

export function listAssets(query: AssetQuery = {}, signal?: AbortSignal) {
  return request<AssetListItem[]>("/assets", { query, signal })
}

export function getAsset(id: string) {
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}`)
}

/* ----------------------------------------------------------- core loop ---- */

/**
 * The one endpoint behind every item barcode. A checked-out item is checked in
 * immediately; anything else comes back as the detail payload, byte-for-byte
 * what `getAsset` returns, so the frontend cannot tell a scan from a click
 * (CLAUDE.md §1.5, design-system.md §8.6).
 */
export function scan(serial: string) {
  return request<ScanResult>("/scan", { method: "POST", body: { serial } })
}

/** Called once per checkout. One transaction: the whole cart, or none of it. */
export function checkout(input: CheckoutInput) {
  return request<CheckoutResult>("/checkout", { method: "POST", body: input })
}

/** Any signed-in user may return any item. `note` is the damage note. */
export function checkIn(assetId: string, note?: string) {
  const trimmed = note?.trim()
  return request<CheckInResult>(`/assets/${encodeURIComponent(assetId)}/checkin`, {
    method: "POST",
    body: trimmed ? { note: trimmed } : {},
  })
}

/* ------------------------------------------------------------- custody ---- */

/** Admin. The roster of who has what — not the current holder of a named item. */
export function activeCustody() {
  return request<CustodyRecord[]>("/custody/active")
}

/** Admin. Sorted most-late first, which is what the overdue screen shows. */
export function overdueCustody() {
  return request<CustodyRecord[]>("/custody/overdue")
}

/** Admin. The full past-custodian trail for one asset. */
export function assetHistory(assetId: string) {
  return request<CustodyRecord[]>(`/assets/${encodeURIComponent(assetId)}/history`)
}

/** Own history, or anyone's if the actor is an admin. */
export function userHistory(userId: string) {
  return request<CustodyRecord[]>(`/users/${encodeURIComponent(userId)}/history`)
}

/* --------------------------------------------------------------- users ---- */

export function listUsers() {
  return request<Profile[]>("/users")
}

export function getUser(id: string) {
  return request<Profile>(`/users/${encodeURIComponent(id)}`)
}

/** The account starts with no password; it sets one at its first scan login. */
export function createUser(input: UserInput) {
  return request<Profile>("/users", { method: "POST", body: input })
}

export function updateUser(id: string, input: UserInput) {
  return request<Profile>(`/users/${encodeURIComponent(id)}`, { method: "PUT", body: input })
}

export function deleteUser(id: string) {
  return request<{ ok: boolean }>(`/users/${encodeURIComponent(id)}`, { method: "DELETE" })
}

/** Admin reset. The server drops that user's sessions itself. */
export function setUserPassword(id: string, password: string) {
  return request<{ ok: boolean }>(`/users/${encodeURIComponent(id)}/password`, {
    method: "POST",
    body: { password },
  })
}

/**
 * Roster CSV import. Relative `photo_path` values in the CSV resolve against
 * `photoDir`, which is why a roster with photos has to come in as multipart.
 */
export function importRoster(file: File, photoDir?: string) {
  const form = new FormData()
  form.set("file", file)
  if (photoDir) form.set("photo_dir", photoDir)
  return request<RosterResult>("/users/import", { method: "POST", form })
}

/* -------------------------------------------------------------- assets ---- */

export function createAsset(input: AssetInput) {
  return request<AssetDetail>("/assets", { method: "POST", body: input })
}

export function updateAsset(id: string, input: AssetInput) {
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: input,
  })
}

/**
 * Refused with 409 for anything that has ever been checked out, because
 * `custody_events` cascades and the trail is the point. The message says to
 * mark it unavailable instead; show it as written.
 */
export function deleteAsset(id: string) {
  return request<{ ok: boolean }>(`/assets/${encodeURIComponent(id)}`, { method: "DELETE" })
}

/** The available/unavailable toggle. `checked_out` is not an accepted value. */
export function setAssetStatus(id: string, status: Extract<AssetStatus, "available" | "unavailable">) {
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}/status`, {
    method: "POST",
    body: { status },
  })
}

/** 10 MB cap, `.jpg .jpeg .png .gif .webp` only, enforced server-side. */
export function setAssetPhoto(id: string, photo: File) {
  const form = new FormData()
  form.set("photo", photo)
  return request<AssetDetail>(`/assets/${encodeURIComponent(id)}/photo`, {
    method: "POST",
    form,
  })
}

/* ---------------------------------------------------------- categories ---- */

export function createCategory(input: CategoryInput) {
  return request<Category>("/categories", { method: "POST", body: input })
}

export function updateCategory(id: string, input: CategoryInput) {
  return request<Category>(`/categories/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: input,
  })
}

/** Refused when the node has children or assets. Say which. */
export function deleteCategory(id: string) {
  return request<{ ok: boolean }>(`/categories/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
}

/* --------------------------------------------------------------- admin ---- */

/** Runs the CSV export into BACKUP_DIR. 503 when that is unset in `.env`. */
export function backupNow() {
  return request<BackupResult>("/admin/backup", { method: "POST" })
}
