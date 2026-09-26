import { describe, expect, it } from "vitest"
import { router } from "./router.svelte"

// The activity timeline keeps its filters in the URL, so an item's "closet
// activity since its last return" link lands on the filtered timeline and a
// reload keeps it (ROADMAP §2.5).
describe("router: the activity timeline's query", () => {
  it("round-trips a filter through the hash", () => {
    const from = "2026-09-20T08:00:00.000Z"
    const href = router.href({ name: "admin", tab: "activity", query: { from, type: "closet,scan" } })
    expect(href).toBe(`#/admin/activity?from=${encodeURIComponent(from)}&type=closet%2Cscan`)
  })

  it("keeps a bare admin tab bare", () => {
    expect(router.href({ name: "admin", tab: "activity" })).toBe("#/admin/activity")
    expect(router.href({ name: "admin", tab: "settings" })).toBe("#/admin/settings")
  })
})
