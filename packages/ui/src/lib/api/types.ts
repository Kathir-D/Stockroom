/**
 * TypeScript mirrors of the JSON the Go server sends and accepts.
 *
 * The authority is `internal/stockroom/types.go` plus the DTOs beside each
 * feature (`assets.go`, `custody.go`, `categories.go`, `auth.go`, ...), and the
 * route table in `CLAUDE.md` §8.1. Field names here are the Go `json` tags
 * verbatim, so a mismatch is a compile error rather than an `undefined` that
 * renders as blank. A Go pointer field is `T | null`, not `T | undefined`:
 * `encoding/json` writes an explicit `null`.
 *
 * Timestamps are RFC 3339 strings as they arrive on the wire. Parsing happens
 * at the edge of a component, never in this file, so nothing here has to guess
 * a timezone.
 */

export type AssetStatus =
  | "available"
  | "checked_out"
  | "unavailable"
  // Present in the Postgres enum and therefore reachable in a payload, but
  // nothing in v1 writes them (CLAUDE.md §6.2).
  | "reserved"
  | "maintenance"
  | "retired"
  | "lost"

/** The three statuses v1 actually uses, in the order the browse list sorts them. */
export const V1_STATUSES: AssetStatus[] = ["available", "checked_out", "unavailable"]

export interface Profile {
  id: string
  email: string | null
  full_name: string | null
  role: string
  student_number: string | null
  first_name: string | null
  last_name: string | null
  photo_path: string | null
  is_admin: boolean
  created_at: string
  /** photo_path under `/files/`, derived server-side. Never built by hand. */
  photo_url: string | null
}

export interface Category {
  id: string
  name: string
  parent_id: string | null
  /** Position among siblings, ascending, name breaking a tie. */
  sort_order: number
  created_at: string
}

/** One node of `GET /categories/tree`: Type -> Category -> Model, three deep. */
export interface CategoryNode extends Category {
  children: CategoryNode[]
}

/** A single hop of an asset's `category_path`, root first. */
export interface CategoryRef {
  id: string
  name: string
}

export interface Asset {
  id: string
  asset_tag: string
  name: string
  description: string | null
  category_id: string | null
  location_id: string | null
  status: AssetStatus
  condition: string | null
  /** The scan key. Barcode stickers encode this. */
  serial_number: string | null
  purchase_date: string | null
  purchase_price: number | null
  warranty_expiration: string | null
  custom_fields: Record<string, unknown> | null
  photo_path: string | null
  created_by: string | null
  created_at: string
  updated_at: string
}

/**
 * The open custody event on an asset, or absent when nobody has it.
 *
 * `student_number` is null for a non-admin viewer and that is deliberate, not a
 * gap: the number signs its owner in by scan with no password, so a browse list
 * carrying one per checked-out unit would be a list of usable credentials
 * (CLAUDE.md §13, 2026-09-13). The custodian's *name* is open to every
 * signed-in user, which is the part the visibility decision was about.
 */
export interface AssetCustody {
  custody_event_id: string
  custodian_id: string
  custodian_name: string
  student_number: string | null
  checked_out_at: string
  due_at: string | null
  overdue: boolean
}

/**
 * One row of `GET /assets`, and also the whole of `GET /assets/{id}`:
 * `AssetDetail` is an alias for `AssetListItem` server-side, because once every
 * row carries the current holder the detail popup knows nothing a row doesn't
 * (CLAUDE.md §13, 2026-09-13).
 */
export interface AssetListItem extends Asset {
  category_path: CategoryRef[]
  photo_url: string | null
  custody: AssetCustody | null
}

export type AssetDetail = AssetListItem

export type ScanAction = "checked_in" | "detail"

export interface ScanResult {
  action: ScanAction
  asset: AssetDetail
  /**
   * Whether this scan may lead to the cart. True only on the detail branch for
   * an available asset: an unavailable unit can't be borrowed, and an item that
   * was just checked in gets a confirmation, not an add screen.
   */
  checkable: boolean
  /** Who held the item, set only when `action` is `checked_in`. */
  returned_from: AssetCustody | null
}

export interface CheckInResult {
  asset: AssetDetail
  returned_from: AssetCustody
}

export interface CheckoutInput {
  /** Blank means the actor, which is the only value a non-admin may send. */
  custodian_id?: string
  asset_ids: string[]
  /** RFC 3339. Must be in the future and at most MAX_CHECKOUT_DAYS away. */
  due_at: string
  /** Admin-only. A non-admin sending it is a 403, not a dropped field. */
  override_overdue?: boolean
}

export interface CheckoutItem {
  custody_event_id: string
  asset_id: string
  asset_tag: string
  name: string
  serial_number: string | null
}

export interface CheckoutResult {
  custodian_id: string
  custodian_name: string
  due_at: string
  items: CheckoutItem[]
}

/** One row of the custody lists and of both history reads. */
export interface CustodyRecord {
  id: string
  asset_id: string
  asset_name: string
  asset_tag: string
  serial_number: string | null

  custodian_id: string
  custodian_name: string
  custodian_student_number: string | null

  checked_out_by: string
  checked_out_by_name: string
  checked_out_at: string

  due_at: string | null
  checked_in_at: string | null
  checked_in_by: string | null
  checked_in_by_name: string | null

  condition_out: string | null
  condition_in: string | null
  notes: string | null

  /** True only while the item is still out and past due; a late return is not. */
  overdue: boolean
  /** Whole days past due, to the return for a closed event, to now for an open one. */
  days_overdue: number
}

export interface LoginResult {
  token: string
  /** The token is limited: only set-password, logout and /me will answer. */
  needs_password: boolean
  has_overdue: boolean
  profile: Profile
}

export interface MeResult {
  profile: Profile
  has_overdue: boolean
}

export interface UserInput {
  student_number: string
  first_name: string
  last_name: string
  email?: string | null
  is_admin: boolean
}

/**
 * The asset create/update body. No `photo_path` and no `status` on purpose:
 * `POST /assets/{id}/photo` is that column's only writer and
 * `POST /assets/{id}/status` is the toggle (CLAUDE.md §13, "one writer per
 * table"). Dates are `YYYY-MM-DD`.
 */
export interface AssetInput {
  asset_tag: string
  name: string
  description?: string | null
  category_id?: string | null
  serial_number?: string | null
  condition?: string | null
  purchase_date?: string | null
  purchase_price?: number | null
  warranty_expiration?: string | null
}

export interface CategoryInput {
  name: string
  /** Null means a Type, at the root of the tree. On an update this is a move. */
  parent_id?: string | null
  /** Omit to leave the node where it is; a new node lands after its siblings. */
  sort_order?: number | null
}

export type RosterAction = "created" | "updated" | "error"

export interface RosterRow {
  row: number
  student_number: string
  action: RosterAction
  error?: string
}

export interface RosterResult {
  created: number
  updated: number
  failed: number
  rows: RosterRow[]
}

export interface TableExport {
  table: string
  file: string
  rows: number
}

export interface BackupResult {
  dir: string
  tables: TableExport[]
  rows: number
  ran_at: string
}

export interface HealthResult {
  ok: boolean
  db: string
  time: string
}
