/**
 * The status system (design-system.md §6): six states, three of them on the
 * asset and three derived here.
 *
 * Hue means state and nothing else in this app — green, blue, orange, amber, red
 * and grey are reserved for exactly these six, and nothing decorative is ever
 * coloured (§1.2). That rule only holds if there is one place that decides which
 * state an asset is in, which is this file. Colour is also never the only signal
 * (§1.3): every state carries its word, and `<StatusDot>` never renders the dot
 * without the label.
 *
 * Two of the six are about the *viewer*, not the asset: an item that is out
 * reads blue when the person looking at it is the one holding it and orange when
 * somebody else is. Which is why `resolveStatus` takes a viewer id — the same
 * row is honestly two different things to two different people, and on a shared
 * closet machine "is that mine?" is the question the browse list is scanned for.
 */

import type { AssetCustody, AssetListItem, AssetStatus } from "./api/types"

export type StatusState =
  | "available"
  | "out"
  | "out-other"
  | "due-soon"
  | "overdue"
  | "unavailable"

/**
 * How close to its due date a checked-out item has to be to read "due soon".
 *
 * A UI-only convention that needs no backend support: the due date is already in
 * every payload that carries custody. Named here rather than buried in a screen
 * so the two apps can't drift on it (§6).
 */
export const DUE_SOON_WINDOW_MS = 24 * 60 * 60 * 1000

export interface ResolvedStatus {
  state: StatusState
  /** The word. Never omitted, never replaced by the dot alone. */
  label: string
  /** Tailwind text colour utility for the label and the dot. */
  fg: string
  /** Tailwind background utility, for the filled-chip expression (§6, C2). */
  bg: string
}

const PRESENTATION: Record<StatusState, Pick<ResolvedStatus, "fg" | "bg">> = {
  available: { fg: "text-status-available", bg: "bg-status-available-bg" },
  out: { fg: "text-status-out", bg: "bg-status-out-bg" },
  "out-other": { fg: "text-status-out-other", bg: "bg-status-out-other-bg" },
  "due-soon": { fg: "text-status-due-soon", bg: "bg-status-due-soon-bg" },
  overdue: { fg: "text-status-overdue", bg: "bg-status-overdue-bg" },
  unavailable: { fg: "text-status-unavailable", bg: "bg-status-unavailable-bg" },
}

/** `Sep 12`. Short because it sits inside a table row, not a sentence. */
export function shortDate(iso: string | null | undefined): string {
  if (!iso) return ""
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ""
  return date.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

/** `Sep 12, 3:40 PM`, for a history row where the time of day matters. */
export function dateTime(iso: string | null | undefined): string {
  if (!iso) return ""
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ""
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  })
}

/**
 * A byte count as a person would say it: `4.2 MB`, `1.1 GB`.
 *
 * Here beside the other formatters rather than in a module of its own, because
 * the backup and photo-mirror screens are the only callers and a second
 * formatting home is how `Sep 12` and `12 Sep` end up on adjacent screens.
 * Base 1024 with the short units, matching what the operating system's file
 * browser shows for the same folder — a backup screen that disagrees with
 * Explorer about the size of a directory is a backup screen nobody trusts.
 */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return "—"
  if (bytes < 1024) return `${Math.max(0, Math.round(bytes))} B`
  const units = ["KB", "MB", "GB", "TB", "PB"]
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  // One decimal below 10, none above: `4.2 MB` is useful, `412.7 MB` is noise.
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`
}

/**
 * How long ago something happened, from an age already measured in hours.
 *
 * The server sends `age_hours` rather than letting the client subtract two
 * clocks, because the closet PC's clock and the browser's are the same clock
 * only by coincidence. This just words it.
 */
export function ageWords(hours: number | null | undefined): string {
  if (hours === null || hours === undefined || !Number.isFinite(hours)) return "never"
  if (hours < 1) {
    const minutes = Math.max(1, Math.round(hours * 60))
    return `${minutes} min ago`
  }
  if (hours < 48) return `${Math.round(hours)} h ago`
  return `${Math.round(hours / 24)} days ago`
}

/** Whole days between now and `iso`, negative once it's in the past. */
function daysUntil(iso: string): number {
  return Math.ceil((new Date(iso).getTime() - Date.now()) / (24 * 60 * 60 * 1000))
}

/**
 * Calendar days from today to `date`: 0 today, 1 tomorrow, -1 yesterday.
 *
 * "Due today" is a statement about the date on the wall calendar, not about a
 * 24-hour window. An item due at 11pm tonight is due *today* even though it is
 * two hours away, and one due at 9am tomorrow is due *tomorrow* even though it
 * is fourteen. Rounding a duration answers neither.
 */
function calendarDaysUntil(date: Date): number {
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  return Math.round((startOfDay(date) - startOfDay(new Date())) / (24 * 60 * 60 * 1000))
}

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`
}

/**
 * A person as a row label: `Jordan Smith` becomes `Jordan S`
 *
 * The status line sits in a fixed-width column beside a name, a serial and a
 * date, so a full name is the one thing in it that can be arbitrarily long. The
 * surname is reduced to an initial rather than truncated, because a clipped
 * `Jordan Smithers…` is both longer and less certain than `Jordan S` — and the
 * full name is still one hover away in the tooltip (`custodianLine`).
 *
 * **No trailing period** (2026-09-16, by preference). The row already uses `·`
 * as its separator, so a period beside it was a second punctuation mark doing no
 * work.
 *
 * Left alone when there is no surname to abbreviate: a mononym, and the
 * student-number fallback `displayName` produces for a profile with no name at
 * all (internal/stockroom/assets.go), are returned as they arrived. A middle
 * name survives too (`Mary Jane Watson` → `Mary Jane W`); the initial is always
 * taken from the last token, which is the part that identifies a family.
 */
export function abbreviateName(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length < 2) return name.trim()
  // A surname already entered as an initial ("Jordan S." on a roster row) loses
  // its period here too, so one name renders one way whatever the source.
  const last = parts[parts.length - 1].replace(/\.+$/, "")
  return [...parts.slice(0, -1), last.charAt(0).toUpperCase()].join(" ")
}

/**
 * Decide the state and the label for one unit, as seen by one viewer.
 *
 * `custody` is the open custody event, which is what actually says whether an
 * item is out: the server reads the row rather than trusting `assets.status`, so
 * a drifted status still reports the real holder (CLAUDE.md §13). This follows
 * the same order for the same reason.
 *
 * `viewerId` splits "out" in two (see the file comment). It is optional and
 * defaults to null, which reads *every* checked-out item as somebody else's —
 * the safe default for the one caller that has no viewer in scope, since the
 * only thing it can get wrong is showing an unfamiliar name that is your own.
 *
 * Overdue and due-soon stay red and amber for everyone, holder or not: they are
 * statements about a deadline, and a late item is late no matter who has it.
 * They name the holder in their label so the ownership question is still
 * answered, just not by hue.
 */
export function resolveStatus(
  asset: Pick<AssetListItem, "status"> & { custody?: AssetCustody | null },
  viewerId: string | null = null
): ResolvedStatus {
  const custody = asset.custody ?? null

  if (custody) {
    const mine = viewerId !== null && custody.custodian_id === viewerId
    /** ` by Jordan S`, or nothing at all when the viewer is the holder. */
    const by = mine ? "" : ` by ${abbreviateName(custody.custodian_name)}`

    if (custody.overdue) {
      const late = custody.due_at ? Math.abs(daysUntil(custody.due_at)) : 0
      const how = late > 0 ? `Overdue ${plural(late, "day")}` : "Overdue"
      return {
        state: "overdue",
        label: mine ? how : `${how} · ${abbreviateName(custody.custodian_name)}`,
        ...PRESENTATION.overdue,
      }
    }
    if (custody.due_at) {
      const remaining = new Date(custody.due_at).getTime() - Date.now()
      if (remaining <= DUE_SOON_WINDOW_MS) {
        const days = calendarDaysUntil(new Date(custody.due_at))
        const when = days <= 0 ? "Due today" : days === 1 ? "Due tomorrow" : "Due soon"
        return {
          state: "due-soon",
          label: mine ? when : `${when} · ${abbreviateName(custody.custodian_name)}`,
          ...PRESENTATION["due-soon"],
        }
      }
      return {
        state: mine ? "out" : "out-other",
        label: `Checked out${by} · due ${shortDate(custody.due_at)}`,
        ...(mine ? PRESENTATION.out : PRESENTATION["out-other"]),
      }
    }
    return {
      state: mine ? "out" : "out-other",
      label: `Checked out${by}`,
      ...(mine ? PRESENTATION.out : PRESENTATION["out-other"]),
    }
  }

  if (asset.status === "unavailable") {
    return { state: "unavailable", label: "Unavailable", ...PRESENTATION.unavailable }
  }
  if (asset.status === "available") {
    return { state: "available", label: "Available", ...PRESENTATION.available }
  }
  // checked_out with no custody row is status drift, and the honest label says
  // so rather than claiming the item is on the shelf. The server's check-in and
  // checkout paths both follow the row, so this is visible, not load-bearing.
  return { state: "unavailable", label: statusWord(asset.status), ...PRESENTATION.unavailable }
}

/** A bare `asset_status` value as a word, for the admin table's status column. */
export function statusWord(status: AssetStatus): string {
  switch (status) {
    case "available":
      return "Available"
    case "checked_out":
      return "Checked out"
    case "unavailable":
      return "Unavailable"
    default:
      return status.charAt(0).toUpperCase() + status.slice(1)
  }
}

/**
 * The count line on a model row: `4 of 6 available`.
 *
 * Its dot reads available when the count is above zero and unavailable at
 * `0 of n`, where **Add** is disabled (§8.2).
 */
export function groupStatus(availableCount: number, total: number): ResolvedStatus {
  const state: StatusState = availableCount > 0 ? "available" : "unavailable"
  return {
    state,
    label: `${availableCount} of ${total} available`,
    ...PRESENTATION[state],
  }
}

/**
 * What a unit row says about who holds it.
 *
 * `You · Sep 12` for the viewer's own item, `Jordan Smith · Sep 15` for anyone
 * else's. The current holder's *name* is open to every signed-in user; their
 * student number is not, and is null in the payload for a non-admin (§8.3,
 * CLAUDE.md §13, 2026-09-13).
 */
export function custodianLine(custody: AssetCustody, viewerId: string | null): string {
  const who = custody.custodian_id === viewerId ? "You" : custody.custodian_name
  const when = shortDate(custody.due_at)
  return when ? `${who} · ${when}` : who
}
