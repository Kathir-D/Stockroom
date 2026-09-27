/**
 * The install's rules the UI has to agree with, from the one public read the
 * sign-in screen already makes (`GET /signin/config`): what a student number
 * looks like, how long an idle session lasts, and when a loan is due.
 *
 * Every value has a default that matches a fresh install, so a screen that
 * renders before the answer arrives, or on a server that cannot give one,
 * still behaves. The server enforces all of these regardless.
 */

import * as api from "../api/index"
import { DIGITS_RULE, ruleFrom, type StudentNumberRule } from "../student-number"

export const DEFAULT_DUE_TIME = "15:30"

class RulesStore {
  studentNumber = $state<StudentNumberRule>(DIGITS_RULE)
  /** Seconds a session survives without a request. */
  idleSeconds = $state(600)
  /** How many days after today the last day of use may be. */
  maxCheckoutDays = $state(api.MAX_CHECKOUT_DAYS)
  /** "HH:MM": a loan is due at this time on the next school day. */
  dueTime = $state(DEFAULT_DUE_TIME)

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
  }
}

export const rules = new RulesStore()
