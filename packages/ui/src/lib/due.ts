/**
 * Turning a picked *date* into a due *instant* (CLAUDE.md §7, decided
 * 2026-09-26).
 *
 * The borrower picks the last day they will use the item. It is due at the
 * closing time (`rules.dueTime`, 15:30 unless an admin changed it) on the
 * next school day after that, so an item brought back first thing the next
 * morning is never already overdue. A school day is a weekday that is not
 * one of the closed dates an admin keeps in Settings (`rules.closedDates`).
 *
 * The cap is on the last day of use: today plus `maxCheckoutDays`. The server
 * holds the same rule (`internal/stockroom/due.go`) and decides; this file
 * only keeps the calendar from offering a date the server would refuse.
 */

/** The closing time a fresh install uses; `DefaultDueTime` in due.go. */
export const DEFAULT_DUE_TIME = "15:30"

function parseTime(dueTime: string): [number, number] {
  const valid = /^\d{2}:\d{2}$/.test(dueTime) ? dueTime : DEFAULT_DUE_TIME
  const [h, m] = valid.split(":").map((part) => Number.parseInt(part, 10))
  return [h, m]
}

/** How far closed dates may push a due date; `maxClosedRun` in due.go. */
const MAX_CLOSED_RUN = 120

/** "YYYY-MM-DD" in local time, the shape `closedDates` holds. */
export function isoDay(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

function isSchoolDay(date: Date, closed: ReadonlySet<string>): boolean {
  return date.getDay() !== 0 && date.getDay() !== 6 && !closed.has(isoDay(date))
}

/** The closing time on the first school day after the day `lastDay` falls on. */
export function dueFor(lastDay: Date, dueTime: string, closedDates: readonly string[] = []): Date {
  const [h, m] = parseTime(dueTime)
  const closed = new Set(closedDates)
  const next = new Date(lastDay.getFullYear(), lastDay.getMonth(), lastDay.getDate() + 1, h, m, 0, 0)
  for (let i = 0; !isSchoolDay(next, closed) && i < MAX_CLOSED_RUN; i++) next.setDate(next.getDate() + 1)
  return next
}

/** The RFC 3339 instant to send for a picked last day of use. */
export function dueInstantFor(lastDay: Date, dueTime: string, closedDates: readonly string[] = []): string {
  return dueFor(lastDay, dueTime, closedDates).toISOString()
}

function startOfDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

/** Whether a date may be picked as the last day of use. */
export function isSelectableLastDay(date: Date, maxDays: number, now = new Date()): boolean {
  const day = startOfDay(date).getTime()
  const today = startOfDay(now)
  const last = new Date(today.getFullYear(), today.getMonth(), today.getDate() + maxDays).getTime()
  return day >= today.getTime() && day <= last
}

/** The latest due instant a checkout made now may have. */
export function latestDue(
  maxDays: number,
  dueTime: string,
  closedDates: readonly string[] = [],
  now = new Date(),
): Date {
  return dueFor(new Date(now.getFullYear(), now.getMonth(), now.getDate() + maxDays), dueTime, closedDates)
}

/** `Tue 6 Oct, 15:30`, for the hint and the picker's button. */
export function dueLabel(due: Date | string): string {
  const date = typeof due === "string" ? new Date(due) : due
  const day = date.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" })
  const time = date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
  return `${day}, ${time}`
}

/** The most closed dates the server keeps; the column's check. */
export const MAX_CLOSED_DATES = 400

function parseDay(text: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text)
  if (!match) return null
  const [y, m, d] = [Number(match[1]), Number(match[2]) - 1, Number(match[3])]
  const date = new Date(y, m, d)
  return date.getFullYear() === y && date.getMonth() === m && date.getDate() === d ? date : null
}

/**
 * Read the closed-dates box: one date or one range per line, as `2026-12-24`
 * or `2026-12-21 to 2027-01-01`. Weekends inside a range are left out, since
 * they are never school days, which keeps a long holiday to its weekdays.
 * Throws with a message naming the line it could not read.
 */
export function parseClosedDates(text: string): string[] {
  const out = new Set<string>()
  for (const raw of text.split("\n")) {
    const line = raw.trim()
    if (!line) continue
    const [fromText, toText, ...rest] = line.split(/\s+to\s+/i)
    const from = parseDay(fromText.trim())
    const to = toText === undefined ? from : parseDay(toText.trim())
    if (!from || !to || rest.length > 0) {
      throw new Error(`"${line}" isn't a date. Write one date as 2026-12-24, or a range as 2026-12-21 to 2027-01-01.`)
    }
    if (to < from) throw new Error(`"${line}" ends before it starts.`)
    for (const day = new Date(from); day <= to; day.setDate(day.getDate() + 1)) {
      if (day.getDay() !== 0 && day.getDay() !== 6) out.add(isoDay(day))
      if (out.size > MAX_CLOSED_DATES) {
        throw new Error(`That is more than ${MAX_CLOSED_DATES} closed weekdays. Keep this year's and next year's.`)
      }
    }
  }
  return [...out].sort()
}

/**
 * The closed dates as the box shows them: a run of weekdays with only a
 * weekend between them reads as one range, so a holiday saved from a range
 * comes back as that range.
 */
export function formatClosedDates(dates: readonly string[]): string {
  const days = [...new Set(dates)]
    .sort()
    .map(parseDay)
    .filter((d): d is Date => d !== null)
  const lines: string[] = []
  let start: Date | null = null
  let end: Date | null = null
  const flush = () => {
    if (!start || !end) return
    lines.push(start.getTime() === end.getTime() ? isoDay(start) : `${isoDay(start)} to ${isoDay(end)}`)
  }
  for (const day of days) {
    if (end) {
      const gap = new Date(end)
      gap.setDate(gap.getDate() + 1)
      while (gap.getDay() === 0 || gap.getDay() === 6) gap.setDate(gap.getDate() + 1)
      if (gap.getTime() === day.getTime()) {
        end = day
        continue
      }
    }
    flush()
    start = day
    end = day
  }
  flush()
  return lines.join("\n")
}
