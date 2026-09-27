/**
 * Turning a picked *date* into a due *instant* (CLAUDE.md §7, decided
 * 2026-09-26).
 *
 * The borrower picks the last day they will use the item. It is due at the
 * closing time (`rules.dueTime`, 15:30 unless an admin changed it) on the
 * next school day after that, so an item brought back first thing the next
 * morning is never already overdue. A school day is a weekday; holidays are
 * not known to the app.
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

/** The closing time on the first weekday after the day `lastDay` falls on. */
export function dueFor(lastDay: Date, dueTime: string): Date {
  const [h, m] = parseTime(dueTime)
  const next = new Date(lastDay.getFullYear(), lastDay.getMonth(), lastDay.getDate() + 1, h, m, 0, 0)
  while (next.getDay() === 0 || next.getDay() === 6) next.setDate(next.getDate() + 1)
  return next
}

/** The RFC 3339 instant to send for a picked last day of use. */
export function dueInstantFor(lastDay: Date, dueTime: string): string {
  return dueFor(lastDay, dueTime).toISOString()
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
export function latestDue(maxDays: number, dueTime: string, now = new Date()): Date {
  return dueFor(new Date(now.getFullYear(), now.getMonth(), now.getDate() + maxDays), dueTime)
}

/** `Tue 6 Oct, 15:30`, for the hint and the picker's button. */
export function dueLabel(due: Date | string): string {
  const date = typeof due === "string" ? new Date(due) : due
  const day = date.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" })
  const time = date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
  return `${day}, ${time}`
}
