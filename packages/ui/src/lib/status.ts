/**
 * The status system (design-system.md §6): five states, three of them on the
 * asset and two derived here.
 *
 * Hue means state and nothing else in this app — green, blue, amber, red and
 * grey are reserved for exactly these five, and nothing decorative is ever
 * coloured (§1.2). That rule only holds if there is one place that decides which
 * state an asset is in, which is this file. Colour is also never the only signal
 * (§1.3): every state carries its word, and `<StatusDot>` never renders the dot
 * without the label.
 */

import type { AssetCustody, AssetListItem, AssetStatus } from "./api/types"

export type StatusState = "available" | "out" | "due-soon" | "overdue" | "unavailable"

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
 * Decide the state and the label for one unit.
 *
 * `custody` is the open custody event, which is what actually says whether an
 * item is out: the server reads the row rather than trusting `assets.status`, so
 * a drifted status still reports the real holder (CLAUDE.md §13). This follows
 * the same order for the same reason.
 */
export function resolveStatus(
  asset: Pick<AssetListItem, "status"> & { custody?: AssetCustody | null }
): ResolvedStatus {
  const custody = asset.custody ?? null

  if (custody) {
    if (custody.overdue) {
      const late = custody.due_at ? Math.abs(daysUntil(custody.due_at)) : 0
      return {
        state: "overdue",
        label: late > 0 ? `Overdue ${plural(late, "day")}` : "Overdue",
        ...PRESENTATION.overdue,
      }
    }
    if (custody.due_at) {
      const remaining = new Date(custody.due_at).getTime() - Date.now()
      if (remaining <= DUE_SOON_WINDOW_MS) {
        const days = calendarDaysUntil(new Date(custody.due_at))
        return {
          state: "due-soon",
          label: days <= 0 ? "Due today" : days === 1 ? "Due tomorrow" : "Due soon",
          ...PRESENTATION["due-soon"],
        }
      }
      return {
        state: "out",
        label: `Checked out · due ${shortDate(custody.due_at)}`,
        ...PRESENTATION.out,
      }
    }
    return { state: "out", label: "Checked out", ...PRESENTATION.out }
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
 * `You · Sep 12` for the viewer's own item, `Jordan S. · Sep 15` for anyone
 * else's. The current holder's *name* is open to every signed-in user; their
 * student number is not, and is null in the payload for a non-admin (§8.3,
 * CLAUDE.md §13, 2026-09-13).
 */
export function custodianLine(custody: AssetCustody, viewerId: string | null): string {
  const who = custody.custodian_id === viewerId ? "You" : custody.custodian_name
  const when = shortDate(custody.due_at)
  return when ? `${who} · ${when}` : who
}
