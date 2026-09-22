import { describe, expect, it } from "vitest"
import {
  DIGITS_RULE,
  filterStudentNumber,
  looksLikeStudentNumber,
  ruleFrom,
} from "./student-number"

const ALNUM = ruleFrom({ student_number_format: "alphanumeric", student_number_filter: "[^A-Za-z0-9]" })
const CUSTOM = ruleFrom({ student_number_format: "custom", student_number_filter: "" })

describe("the student-number rule on the sign-in field", () => {
  it("falls back to digits for anything the server did not send properly", () => {
    expect(ruleFrom(undefined)).toEqual(DIGITS_RULE)
    expect(ruleFrom({ student_number_format: "bogus" })).toEqual(DIGITS_RULE)
    expect(ruleFrom({ student_number_format: "custom", student_number_filter: "[" })).toEqual(DIGITS_RULE)
  })

  // The regression that made this file necessary: a card that reads AB12345
  // must survive the field's filter intact, or the scan-vs-typed comparison
  // never matches and every scan asks for a password.
  it("keeps letters under the alphanumeric rule and strips them under digits", () => {
    expect(filterStudentNumber("AB12345", ALNUM)).toBe("AB12345")
    expect(filterStudentNumber("AB12345", DIGITS_RULE)).toBe("12345")
    expect(filterStudentNumber("AB-123", ALNUM)).toBe("AB123")
    expect(filterStudentNumber("anything goes", CUSTOM)).toBe("anything goes")
  })

  it("tells an item barcode from a card by the rule in force", () => {
    expect(looksLikeStudentNumber("123456")).toBe(true)
    expect(looksLikeStudentNumber("AB12345")).toBe(false)
    expect(looksLikeStudentNumber("AB12345", ALNUM)).toBe(true)
    // A dashed serial is an item under both built-in rules.
    expect(looksLikeStudentNumber("SD-014", ALNUM)).toBe(false)
    expect(looksLikeStudentNumber("x".repeat(33), CUSTOM)).toBe(false)
  })
})
