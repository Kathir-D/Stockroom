/**
 * The install's rules the UI has to agree with, from the one public read the
 * sign-in screen already makes (`GET /signin/config`): what a student number
 * looks like, how long an idle session lasts, when a loan is due, and how
 * fast a scanner types.
 *
 * Every value has a default that matches a fresh install, so a screen that
 * renders before the answer arrives, or on a server that cannot give one,
 * still behaves. The server enforces all of these regardless.
 */

import * as api from "../api/index"
import { DEFAULT_DUE_TIME } from "../due"
import { SCAN_KEY_THRESHOLD_MS } from "../scanner"
import { DIGITS_RULE, ruleFrom, type StudentNumberRule } from "../student-number"

class RulesStore {
  studentNumber = $state<StudentNumberRule>(DIGITS_RULE)
  /** Seconds a session survives without a request. */
  idleSeconds = $state(600)
  /** How many days after today the last day of use may be. */
  maxCheckoutDays = $state(api.MAX_CHECKOUT_DAYS)
  /** "HH:MM": a loan is due at this time on the next school day. */
  dueTime = $state(DEFAULT_DUE_TIME)
  /** Dates the school is closed, "YYYY-MM-DD"; a loan never falls due on one. */
  closedDates = $state<string[]>([])
  /** Whether anything overdue stops a checkout. */
  overdueBlocks = $state(true)
  /** The longest gap between keys that still reads as a scanner. */
  scanThresholdMs = $state(SCAN_KEY_THRESHOLD_MS)

  private loaded = false

  /** Fetch once; `force` refetches, after a sign-in, so a changed setting lands. */
  async load(force = false) {
    if (this.loaded && !force) return
    try {
      const raw = (await api.signInConfig()) as Record<string, unknown> | null
      this.adopt(raw)
      this.loaded = true
    } catch {
      // Defaults stand.
    }
  }

  /** Read a `/signin/config` answer. Exposed for the sign-in screen and tests. */
  adopt(raw: Record<string, unknown> | null) {
    this.studentNumber = ruleFrom(raw)
    if (typeof raw?.session_idle_seconds === "number") this.idleSeconds = raw.session_idle_seconds
    const checkout = raw?.checkout as Record<string, unknown> | undefined
    if (typeof checkout?.max_checkout_days === "number") this.maxCheckoutDays = checkout.max_checkout_days
    if (typeof checkout?.due_time === "string" && /^\d{2}:\d{2}$/.test(checkout.due_time)) {
      this.dueTime = checkout.due_time
    }
    if (Array.isArray(checkout?.closed_dates)) {
      this.closedDates = checkout.closed_dates.filter((d): d is string => typeof d === "string")
    }
    if (typeof checkout?.overdue_blocks_checkout === "boolean") {
      this.overdueBlocks = checkout.overdue_blocks_checkout
    }
    if (typeof raw?.scan_threshold_ms === "number") this.scanThresholdMs = raw.scan_threshold_ms
  }
}

export const rules = new RulesStore()
