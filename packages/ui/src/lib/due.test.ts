import { describe, expect, it } from "vitest"
import { dueFor, isSelectableLastDay, latestDue } from "./due"

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
    expect(latestDue(7, "15:30", now)).toEqual(new Date(2026, 9, 6, 15, 30))
  })
})
