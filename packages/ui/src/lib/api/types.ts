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
  | "lost";

/** The three statuses v1 actually uses, in the order the browse list sorts them. */
export const V1_STATUSES: AssetStatus[] = [
  "available",
  "checked_out",
  "unavailable",
];

export interface Profile {
  id: string;
  email: string | null;
  full_name: string | null;
  role: string;
  student_number: string | null;
  first_name: string | null;
  last_name: string | null;
  photo_path: string | null;
  is_admin: boolean;
  created_at: string;
  /** photo_path under `/files/`, derived server-side. Never built by hand. */
  photo_url: string | null;
}

export interface Category {
  id: string;
  name: string;
  parent_id: string | null;
  /** Position among siblings, ascending, name breaking a tie. */
  sort_order: number;
  created_at: string;
}

/** One node of `GET /categories/tree`: Type -> Category -> Model, three deep. */
export interface CategoryNode extends Category {
  children: CategoryNode[];
}

/** A single hop of an asset's `category_path`, root first. */
export interface CategoryRef {
  id: string;
  name: string;
}

export interface Asset {
  id: string;
  /** Internal, database-generated (`AST-000001`). Not shown to students. */
  asset_tag: string;
  name: string;
  description: string | null;
  category_id: string | null;
  location_id: string | null;
  status: AssetStatus;
  condition: string | null;
  /** The scan key. Barcode stickers encode this. */
  serial_number: string | null;
  purchase_date: string | null;
  purchase_price: number | null;
  warranty_expiration: string | null;
  custom_fields: Record<string, unknown> | null;
  photo_path: string | null;
  created_by: string | null;
  created_at: string;
  updated_at: string;
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
  custody_event_id: string;
  custodian_id: string;
  custodian_name: string;
  student_number: string | null;
  checked_out_at: string;
  due_at: string | null;
  overdue: boolean;
}

/**
 * One row of `GET /assets`, and also the whole of `GET /assets/{id}`:
 * `AssetDetail` is an alias for `AssetListItem` server-side, because once every
 * row carries the current holder the detail popup knows nothing a row doesn't
 * (CLAUDE.md §13, 2026-09-13).
 */
export interface AssetListItem extends Asset {
  category_path: CategoryRef[];
  photo_url: string | null;
  custody: AssetCustody | null;
}

export type AssetDetail = AssetListItem;

export type ScanAction = "checked_in" | "detail";

export interface ScanResult {
  action: ScanAction;
  asset: AssetDetail;
  /**
   * Whether this scan may lead to the cart. True only on the detail branch for
   * an available asset: an unavailable unit can't be borrowed, and an item that
   * was just checked in gets a confirmation, not an add screen.
   */
  checkable: boolean;
  /** Who held the item, set only when `action` is `checked_in`. */
  returned_from: AssetCustody | null;
}

export interface CheckInResult {
  asset: AssetDetail;
  returned_from: AssetCustody;
}

export interface CheckoutInput {
  /** Blank means the actor, which is the only value a non-admin may send. */
  custodian_id?: string;
  asset_ids: string[];
  /** RFC 3339. Must be in the future and at most MAX_CHECKOUT_DAYS away. */
  due_at: string;
  /** Admin-only. A non-admin sending it is a 403, not a dropped field. */
  override_overdue?: boolean;
}

export interface CheckoutItem {
  custody_event_id: string;
  asset_id: string;
  asset_tag: string;
  name: string;
  serial_number: string | null;
}

export interface CheckoutResult {
  custodian_id: string;
  custodian_name: string;
  due_at: string;
  items: CheckoutItem[];
}

/** One row of the custody lists and of both history reads. */
export interface CustodyRecord {
  id: string;
  asset_id: string;
  asset_name: string;
  asset_tag: string;
  serial_number: string | null;

  custodian_id: string;
  custodian_name: string;
  custodian_student_number: string | null;

  checked_out_by: string;
  checked_out_by_name: string;
  checked_out_at: string;

  due_at: string | null;
  checked_in_at: string | null;
  checked_in_by: string | null;
  checked_in_by_name: string | null;

  condition_out: string | null;
  condition_in: string | null;
  notes: string | null;

  /** True only while the item is still out and past due; a late return is not. */
  overdue: boolean;
  /** Whole days past due, to the return for a closed event, to now for an open one. */
  days_overdue: number;
}

/**
 * Something the person signing in should be told before they start, beside
 * `has_overdue` because it is the same shape of fact and sign-in is the one
 * moment everybody passes through (docs/design/backup.md §E.7).
 *
 * `admins` is names only. A student number is a working scan login
 * (CLAUDE.md §7), so a warning carrying one would hand every student an admin
 * credential.
 */
export interface BackupWarning {
  message: string;
  admins: string[];
}

export interface LoginResult {
  token: string;
  /** The token is limited: only set-password, logout and /me will answer. */
  needs_password: boolean;
  has_overdue: boolean;
  profile: Profile;
  backup_warning: BackupWarning | null;
}

export interface MeResult {
  profile: Profile;
  has_overdue: boolean;
  backup_warning: BackupWarning | null;
}

export interface UserInput {
  student_number: string;
  first_name: string;
  last_name: string;
  email?: string | null;
  is_admin: boolean;
}

/**
 * The asset create/update body. No `photo_path`, no `status` and no `asset_tag`
 * on purpose: `POST /assets/{id}/photo` is that column's only writer,
 * `POST /assets/{id}/status` is the toggle, and the asset tag is generated by
 * the database and never sent by a client (CLAUDE.md §13, "one writer per
 * table"). Dates are `YYYY-MM-DD`.
 *
 * `serial_number` is required: it is the scan key, and an asset without one
 * cannot be checked in.
 */
export interface AssetInput {
  name: string;
  description?: string | null;
  category_id?: string | null;
  serial_number: string;
  condition?: string | null;
  purchase_date?: string | null;
  purchase_price?: number | null;
  warranty_expiration?: string | null;
}

/**
 * A kit: a named bundle of units that goes out and comes back together
 * (CLAUDE.md §2, TODO Phase 8).
 *
 * A kit never holds custody of anything. Adding one to the cart expands it into
 * its asset ids and `POST /checkout` commits them like any other cart, so the
 * 7-day cap, the overdue block and the custodian rule cannot differ between a
 * kit and a handful of units.
 */
export interface Kit {
  id: string;
  name: string;
  description: string | null;
  created_at: string;
}

/** `GET /kits` and `GET /kits/{id}`: the kit with its units as browse rows. */
export interface KitDetail extends Kit {
  /** Browse order — category document order, available first. */
  items: AssetListItem[];
  available: number;
  checked_out: number;
  unavailable: number;
  /**
   * True only when every unit is on the shelf. A kit is taken whole or not at
   * all: half a kit is a camera with no lens, and the person holding it finds
   * out at the shoot. An empty kit is not checkable either.
   */
  checkable: boolean;
}

/** Membership is not part of it: units go in and out one press at a time. */
export interface KitInput {
  name: string;
  description?: string | null;
}

/** One unit named in a kit result, without the whole row. */
export interface KitItemRef {
  asset_id: string;
  name: string;
  serial_number: string | null;
}

export interface KitReturnProblem extends KitItemRef {
  reason: string;
}

/**
 * What `POST /kits/{id}/checkin` did, unit by unit.
 *
 * Three buckets rather than a count, because a kit return is deliberately not
 * all-or-nothing: the units are on the counter, and refusing all four because
 * one was already back would leave the database claiming somebody still holds
 * items they returned. Every list is present, never null.
 */
export interface KitCheckInResult {
  kit: Kit;
  returned: CheckInResult[];
  already_in: KitItemRef[];
  failed: KitReturnProblem[];
}

export interface CategoryInput {
  name: string;
  /** Null means a Type, at the root of the tree. On an update this is a move. */
  parent_id?: string | null;
  /** Omit to leave the node where it is; a new node lands after its siblings. */
  sort_order?: number | null;
}

export type RosterAction = "created" | "updated" | "error";

export interface RosterRow {
  row: number;
  student_number: string;
  action: RosterAction;
  error?: string;
}

export interface RosterResult {
  created: number;
  updated: number;
  failed: number;
  rows: RosterRow[];
}

export interface TableExport {
  table: string;
  /** The path *inside the archive*: the raw tables live in the zip. */
  file: string;
  rows: number;
}

/** One off-site push. A failure here is never the run's failure. */
export interface TargetResult {
  target: string;
  ok: boolean;
  /** What the target calls what it wrote: a commit sha, a folder. */
  ref: string;
  error: string;
}

export interface PhotoMirrorResult {
  configured: boolean;
  generation: string;
  rolled_over: boolean;
  copied: number;
  unchanged: number;
  bytes: number;
  error: string;
}

export interface BackupResult {
  dir: string;
  /** Full path to the restorable archive the run wrote. */
  archive: string;
  ran_at: string;
  source: string;
  tables: TableExport[];
  rows: number;
  schema_version: string;
  encrypted: boolean;
  /** Another process held the backup lock; this run did nothing, on purpose. */
  skipped: boolean;
  targets: TargetResult[];
  photos: PhotoMirrorResult | null;
  pruned: string[];
}

/**
 * Backup configuration (docs/design/backup.md §C.2). It lives in the database
 * rather than `.env` so no admin ever edits a file.
 *
 * The two secrets come back **blank** with a `_set` boolean beside them, never
 * masked with asterisks: a mask round-trips, and the panel would eventually
 * write it back as the literal new token. Leaving a secret field empty on save
 * means "leave it alone".
 */
export interface Settings {
  backup_dir: string;
  photo_backup_dir: string;
  keep_days: number;
  stale_hours: number;
  schedule_hour: number;
  drive_enabled: boolean;
  drive_remote: string;
  drive_path: string;
  /**
   * The school's own Google OAuth client, for the one Google connection the
   * backup and the photo wall share. Blank is rclone's shared client, which
   * rclone is retiring during 2026. The secret comes back blank, like the token.
   */
  google_client_id: string;
  google_client_secret: string;
  github_enabled: boolean;
  github_repo: string;
  github_token: string;
  archive_passphrase: string;
  photo_min_free_gb: number;
  photo_max_generations: number;
  /** What a student number may look like (lib/student-number.ts). */
  student_number_format: "digits" | "alphanumeric" | "custom";
  /** Only meaningful when the format is custom; kept when switching away. */
  student_number_pattern: string;
  updated_at: string;
  github_token_set: boolean;
  archive_passphrase_set: boolean;
  google_client_secret_set: boolean;
}

/** A partial update: an omitted field is left alone. */
export type SettingsInput = Partial<
  Omit<
    Settings,
    "updated_at" | "github_token_set" | "archive_passphrase_set" | "google_client_secret_set"
  >
>;

export interface BackupTargetStatus {
  target: string;
  enabled: boolean;
  last_success: string | null;
  age_hours: number | null;
  ref: string;
  last_error: string;
  last_error_at: string | null;
}

export interface PhotoGeneration {
  name: string;
  at: string;
  files: number;
  bytes: number;
  /** The generation the next mirror run writes into. It cannot be deleted. */
  live: boolean;
}

export interface PhotoMirrorStatus {
  configured: boolean;
  dir: string;
  current: string;
  generations: PhotoGeneration[];
  bytes: number;
  free_bytes: number;
  min_free_gb: number;
  max_generations: number;
  warnings: string[];
}

export interface BackupStatusResult {
  configured: boolean;
  dir: string;
  stale_hours: number;
  stale: boolean;
  worst_age_hours: number | null;
  targets: BackupTargetStatus[];
  last_run_at: string | null;
  last_run_source: string;
  /**
   * Whether a failsafe admin exists. When it does not, a restored-from-empty
   * database has nobody to sign in as and the admin panel — the whole
   * documented restore route — is unreachable (§C.1).
   */
  failsafe_admin_configured: boolean;
  photo_mirror: PhotoMirrorStatus | null;
  /** Already-worded sentences. The UI decides how to render them, not what they say. */
  warnings: string[];
  log: string[];
  schedule: string;
}

/** One past backup a target can hand back. `id` is opaque to the UI. */
export interface BackupVersion {
  id: string;
  label: string;
  at: string;
  bytes: number;
  note: string;
}

export interface RestoreResult {
  ran_at: string;
  source: string;
  /** An account id, or `cli`. The log's identifier, not a label for a screen. */
  by: string;
  /** The same person in words, resolved before the tables were replaced. */
  by_name: string;
  schema_version: string;
  archive_ran_at: string;
  tables: TableExport[];
  rows: number;
  sequences: number;
  /** What could not be checked, rather than a pretence that it was. */
  warnings: string[];
}

export interface DriveConnectResult {
  url: string;
  id: string;
  /** What to run elsewhere when the link will not open here; carries the scope. */
  paste_command: string;
}

export interface HealthResult {
  ok: boolean;
  db: string;
  time: string;
}

/**
 * The sign-in photo wall's batch (docs/design/signin-photo-wall.html §5).
 *
 * `photos` is always an array and is often empty -- the wall is off, the
 * manifest is still building, Drive is unreachable, or a burst of sign-ins
 * drained the reel. All of those mean the same thing to the only caller: draw
 * no columns. There is no error shape here because the endpoint never returns
 * one.
 */
export interface SignInPhotos {
  /** Server-relative tile paths, e.g. `/signin-photos/9f2c….jpg`. */
  photos: string[];
  /** How long those URLs stay fetchable before the server deletes the files. */
  ttl_seconds: number;
}

/**
 * The admin screen's read of the photo wall
 * (docs/design/signin-photo-wall.html §7).
 *
 * There is deliberately no folder id and no Drive URL on this shape, and
 * there must never be one. A Drive folder link is a capability, not a label:
 * a folder shared "anyone with the link" is readable by whoever holds the
 * URL, so echoing it into a screen on a shared closet machine would put the
 * whole folder one copy-paste away from anyone who walks up to it. What
 * identifies the folder here is `folder_label`, a name a person typed, plus
 * the preview strip -- which answers the question an admin actually has
 * ("are the right photographs showing?") better than an id could.
 */
export interface PhotoWallStatus {
  /** Whether the wall is running. Off until Google is signed in to. */
  enabled: boolean;
  rclone_installed: boolean;
  /**
   * Whether rclone has the wall's remote — somebody has signed in to Google
   * for it. This is the switch: the wall starts the moment it becomes true.
   */
  google_connected: boolean;
  /** Whether the Sign in with Google button can work on this machine. */
  can_connect_google: boolean;
  /** The rclone remote's name. A name, never a credential. */
  remote: string;
  /**
   * Whether `setPhotoWallFolder` would get past its first gate. The server
   * reports it rather than letting this screen infer it from the two above,
   * which do not add up to the same condition — so the paste form can be
   * refused before it is typed into instead of after.
   */
  can_set_folder: boolean;

  folder_set: boolean;
  /** The typed name. Never the id. */
  folder_label: string;
  changed_at: string | null;
  changed_by: string;

  /** §3's streamed listing progress: a big folder takes minutes. */
  listing: boolean;
  listed_so_far: number;
  photo_count: number;
  built_at: string | null;

  /** Tiles waiting, and tiles out in a browser waiting to expire. */
  ready: number;
  served: number;

  last_error: string;
  last_error_at: string | null;
}

/* ------------------------------------------------ bulk ways in (Phase B) ---- */

export interface CategoryImportResult {
  created: number;
  existing: number;
  failed: number;
  format: "text" | "csv";
  rows: { path: string[]; created: boolean; error?: string }[];
}

export interface AssetImportResult {
  created: number;
  updated: number;
  failed: number;
  rows: {
    line: number;
    serial_number: string;
    name: string;
    action: "created" | "updated" | "failed";
    error?: string;
    /** On an update: the item that already had this serial. */
    note?: string;
  }[];
}

export interface BulkAddInput {
  name: string;
  prefix: string;
  count: number;
  category_id?: string | null;
  /** Zero-padding of the number; 0 means 3. */
  digits?: number;
  /** 0 means "after the highest already used". */
  start_at?: number;
}

export interface BulkPreview {
  serials: string[];
  existing: string[];
  name: string;
}

export interface LabelLayout {
  key: string;
  label: string;
  paper: string;
  per_sheet: number;
  max_serial_chars: number;
  hint: string;
}

/* ----------------------------------------------------- the setup wizard ---- */

export interface FirstAdminInput {
  student_number: string;
  first_name: string;
  last_name: string;
  password: string;
  student_number_format: "digits" | "alphanumeric" | "custom";
  student_number_pattern: string;
}

export interface SetupState {
  needs_admin: boolean;
  step: number;
  completed: boolean;
  failsafe_configured: boolean;
  failsafe_writable: boolean;
  examples_present: boolean;
}

export interface ExamplesResult {
  categories: CategoryImportResult;
  assets: AssetImportResult;
  people: RosterResult;
  skipped?: string[];
}
