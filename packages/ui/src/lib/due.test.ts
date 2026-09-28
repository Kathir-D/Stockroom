import { describe, expect, it } from "vitest"
import { dueFor, formatClosedDates, isSelectableLastDay, latestDue, parseClosedDates } from "./due"

describe("dueFor", () => {
  it("is the closing time on the next weekday", () => {
    // Local dates: Monday 28 September 2026 and Friday 2 October 2026.
    const monday = new Date(2026, 8, 28)
    expect(dueFor(monday, "15:30")).toEqual(new Date(2026, 8, 29, 15, 30))
    const friday = new Date(2026, 9, 2)
    expect(dueFor(friday, "15:30")).toEqual(new Date(2026, 9, 5, 15, 30))
    const saturday = new Date(2026, 9, 3)
    expect(dueFor(saturday, "08:05")).toEqual(new Date(2026, 9, 5, 8, 5))
  })

  it("is never earlier than the day after the last day of use", () => {
    const now = new Date(2026, 8, 28, 23, 0)
    expect(dueFor(now, "15:30").getTime()).toBeGreaterThan(now.getTime())
  })

  it("skips closed dates, as due.go does", () => {
    const closed = ["2026-09-29", "2026-09-30", "2026-10-05"]
    expect(dueFor(new Date(2026, 8, 28), "15:30", closed)).toEqual(new Date(2026, 9, 1, 15, 30))
    expect(dueFor(new Date(2026, 9, 2), "15:30", closed)).toEqual(new Date(2026, 9, 6, 15, 30))
    expect(latestDue(7, "15:30", closed, new Date(2026, 8, 28, 10, 0))).toEqual(new Date(2026, 9, 6, 15, 30))
  })
})

describe("isSelectableLastDay", () => {
  const now = new Date(2026, 8, 28, 16, 0)
  it("offers today through the cap and nothing else", () => {
    expect(isSelectableLastDay(new Date(2026, 8, 27), 7, now)).toBe(false)
    expect(isSelectableLastDay(new Date(2026, 8, 28), 7, now)).toBe(true)
    expect(isSelectableLastDay(new Date(2026, 9, 5), 7, now)).toBe(true)
    expect(isSelectableLastDay(new Date(2026, 9, 6), 7, now)).toBe(false)
  })

  it("matches latestDue for the last selectable day", () => {
    expect(latestDue(7, "15:30", [], now)).toEqual(new Date(2026, 9, 6, 15, 30))
  })
})

describe("closed dates", () => {
  it("reads single dates and ranges, leaving weekends out", () => {
    expect(parseClosedDates("2026-12-24\n\n 2026-12-25 to 2026-12-29 \n2026-12-24")).toEqual([
      "2026-12-24",
      "2026-12-25",
      "2026-12-28",
      "2026-12-29",
    ])
  })

  it("names a line it can't read", () => {
    expect(() => parseClosedDates("2026-02-30")).toThrow(/2026-02-30/)
    expect(() => parseClosedDates("Christmas")).toThrow(/Christmas/)
    expect(() => parseClosedDates("2026-12-29 to 2026-12-21")).toThrow(/ends before/)
  })

  it("shows a run across a weekend as one range", () => {
    const dates = parseClosedDates("2026-12-21 to 2027-01-01\n2027-02-15")
    expect(formatClosedDates(dates)).toBe("2026-12-21 to 2027-01-01\n2027-02-15")
    expect(parseClosedDates(formatClosedDates(dates))).toEqual(dates)
  })
})
