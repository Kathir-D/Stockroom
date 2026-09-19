/**
 * The kits store's one rule with a decision in it: where a patched row lands.
 *
 * `patch` exists so an edit does not cost a list refetch (see the store's own
 * comment), and that shortcut is only honest if the row ends up where a refetch
 * would have put it. A rename is the case that separates the two, so it is the
 * case worth a test.
 */

import { beforeEach, describe, expect, it } from "vitest"
import type { KitDetail } from "../api/types"
import { kits } from "./kits.svelte"

function kit(id: string, name: string): KitDetail {
  return {
    id,
    name,
    description: null,
    items: [],
    available: 0,
    checked_out: 0,
    checkable: false,
  } as unknown as KitDetail
}

const names = () => kits.kits.map((k) => k.name)

describe("kits store patch", () => {
  beforeEach(() => {
    kits.reset()
    kits.kits = [kit("a", "Alpha"), kit("m", "Mike"), kit("z", "Zebra")]
  })

  it("moves a renamed kit to its new place in the list", () => {
    kits.patch(kit("z", "Bravo"))
    expect(names()).toEqual(["Alpha", "Bravo", "Mike"])
  })

  it("leaves a kit where it was when the name did not change", () => {
    kits.patch(kit("m", "Mike"))
    expect(names()).toEqual(["Alpha", "Mike", "Zebra"])
  })

  it("inserts a kit it has never seen in name order", () => {
    kits.patch(kit("n", "November"))
    expect(names()).toEqual(["Alpha", "Mike", "November", "Zebra"])
  })

  it("does not duplicate the row it is replacing", () => {
    kits.patch(kit("a", "Yankee"))
    expect(kits.kits.filter((k) => k.id === "a")).toHaveLength(1)
    expect(names()).toEqual(["Mike", "Yankee", "Zebra"])
  })
})
