/**
 * Turning a picked *date* into a due *instant*.
 *
 * The 7-day cap is an exact instant — `now + 7 × 24h` at the moment the server
 * handles the request, not "the end of the seventh day" (CLAUDE.md §13,
 * 2026-09-12). A date picker offers a date, so something has to bridge the two,
 * and getting it wrong looks like this: a student picks "seven days out" at
 * 4 p.m., the UI sends 23:59 on that day, and the server refuses a checkout that
 * the calendar plainly offered.
 *
 * So: a picked date means the end of that day, clamped to the cap with a margin
 * for the round trip. The margin is why `SUBMIT_MARGIN_MS` exists — without it,
 * a request that takes 200ms to arrive is 200ms over the line.
 */

import { MAX_CHECKOUT_DAYS } from "./api/index"

const DAY_MS = 24 * 60 * 60 * 1000

/**
 * Slack left between the due instant and the cap, so the clamped case survives
 * the time the request spends in flight. Five minutes is far longer than
 * localhost needs and far shorter than anyone would notice on a 7-day loan.
 */
export const SUBMIT_MARGIN_MS = 5 * 60 * 1000

/** The latest instant the server will accept, as of now. */
export function latestDueInstant(now = new Date()): Date {
  return new Date(now.getTime() + MAX_CHECKOUT_DAYS * DAY_MS)
}

/** The last date a picker may offer, in the viewer's own timezone. */
export function latestDueDate(now = new Date()): Date {
  return latestDueInstant(now)
}

/** Local-midnight-to-23:59:59.999 of whatever day `date` falls on. */
function endOfLocalDay(date: Date): Date {
  const end = new Date(date)
  end.setHours(23, 59, 59, 999)
  return end
}

/**
 * The RFC 3339 instant to send for a picked calendar date.
 *
 * End of the picked day, pulled back to the cap when that would overshoot, which
 * is the ordinary case for the last selectable day.
 */
export function dueInstantFor(date: Date, now = new Date()): string {
  const cap = latestDueInstant(now).getTime() - SUBMIT_MARGIN_MS
  const wanted = endOfLocalDay(date).getTime()
  return new Date(Math.min(wanted, cap)).toISOString()
}

/** Whether a date is inside the window a checkout may use. */
export function isSelectableDueDate(date: Date, now = new Date()): boolean {
  const endOfDay = endOfLocalDay(date).getTime()
  if (endOfDay <= now.getTime()) return false
  const startOfDay = new Date(date)
  startOfDay.setHours(0, 0, 0, 0)
  return startOfDay.getTime() <= latestDueInstant(now).getTime()
}

/** `Sep 21` — the hint under the picker that names the cap. */
export function capHint(now = new Date()): string {
  return latestDueInstant(now).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  })
}
