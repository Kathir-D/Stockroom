/**
 * What a student number may look like, as the sign-in screen needs to know it
 * before anybody has a session (CLAUDE.md §13, Phase B).
 *
 * The server is the authority (`internal/stockroom/student_number.go`); this is
 * the copy the field filters keystrokes with. They must agree, and the failure
 * when they do not is specific and bad: the sign-in screen decides "scan or
 * typed" by asking whether the field holds what the keystroke buffer saw, run
 * through this filter (CLAUDE.md §13, 2026-09-15). A filter stricter than the
 * server's rule strips an `AB12345` card to `12345`, the two never match, and
 * every card scan is demoted to typed -- demanding a password from a
 * roster-imported student who has not got one yet.
 *
 * So the filter is **served**, not duplicated: `GET /signin/config` returns the
 * character class to strip, and until it arrives the historical digits rule
 * stands in, which is exactly what an install that never changed the setting
 * uses anyway.
 */

export type StudentNumberFormat = "digits" | "alphanumeric" | "custom"

export interface StudentNumberRule {
  format: StudentNumberFormat
  /** A regex character class to strip, e.g. `[^0-9]`. Empty strips nothing. */
  filter: string
}

/** Mirrors `MaxStudentNumberLength` in `internal/stockroom/password.go`. */
export const MAX_STUDENT_NUMBER_LENGTH = 32

export const DIGITS_RULE: StudentNumberRule = { format: "digits", filter: "[^0-9]" }

/**
 * Turn what the server sent into a rule, falling back to digits for anything
 * that is not one. A filter that does not compile is dropped rather than
 * thrown: the sign-in screen must never be the thing that breaks.
 */
export function ruleFrom(raw: unknown): StudentNumberRule {
  if (!raw || typeof raw !== "object") return DIGITS_RULE
  const r = raw as Record<string, unknown>
  const format = r.student_number_format
  const filter = typeof r.student_number_filter === "string" ? r.student_number_filter : ""
  if (format !== "digits" && format !== "alphanumeric" && format !== "custom") return DIGITS_RULE
  try {
    if (filter) new RegExp(filter, "g")
  } catch {
    return DIGITS_RULE
  }
  return { format, filter }
}

/** The field's filter: strip what the rule does not allow, cap the length. */
export function filterStudentNumber(value: string, rule: StudentNumberRule): string {
  const kept = rule.filter ? value.replace(new RegExp(rule.filter, "g"), "") : value
  return kept.slice(0, MAX_STUDENT_NUMBER_LENGTH)
}

/**
 * Whether a whole code could be a student number under the rule. Used to tell
 * an item barcode scanned at the sign-in screen from a card (design-system.md
 * §15 Q4). A custom rule is any regex the admin wrote, which cannot be judged
 * here, so anything non-empty passes and the server says no.
 */
export function looksLikeStudentNumber(code: string, rule: StudentNumberRule = DIGITS_RULE): boolean {
  const trimmed = code.trim()
  if (!trimmed || trimmed.length > MAX_STUDENT_NUMBER_LENGTH) return false
  switch (rule.format) {
    case "alphanumeric":
      return /^[A-Za-z0-9]+$/.test(trimmed)
    case "custom":
      return true
    default:
      return /^[0-9]+$/.test(trimmed)
  }
}

/** The sentence for a typed value the rule refuses. */
export function formatHint(rule: StudentNumberRule): string {
  switch (rule.format) {
    case "alphanumeric":
      return "Student numbers are letters and numbers only."
    case "custom":
      return "That doesn't look like a student number."
    default:
      return "Student numbers are digits only."
  }
}
