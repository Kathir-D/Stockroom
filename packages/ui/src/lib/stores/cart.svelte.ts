/**
 * The cart: a frontend-only set of asset ids, never sent to the server until
 * checkout (CLAUDE.md §7).
 *
 * Three rules from the 2026-09-12 session shape this file:
 *
 *  - **A cart is a set.** The same asset twice is one item, not a failed
 *    checkout. The server enforces that too; matching it here means the count in
 *    the dock is the count that will be checked out.
 *  - **It survives a page reload** and clears only on sign-out or the idle
 *    timeout (§15 Q5). Hence sessionStorage rather than in-memory state: an
 *    accidental refresh mid-shopping shouldn't cost someone their trip to the
 *    shelf, and the unattended case is already covered by the 5-minute timeout.
 *  - **It only ever accumulates items being borrowed** (§15 Q3). Scanning a
 *    checked-out item checks it in immediately and never touches the cart, so
 *    nothing in here mixes borrowing with returning.
 */

import type { AssetListItem } from "../api/types"

const STORAGE_KEY = "stockroom_cart"
const DUE_KEY = "stockroom_cart_due"

function storage(): Storage | null {
  try {
    return typeof sessionStorage === "undefined" ? null : sessionStorage
  } catch {
    return null
  }
}

function loadIds(): string[] {
  const raw = storage()?.getItem(STORAGE_KEY)
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === "string") : []
  } catch {
    return []
  }
}

class CartStore {
  /** Insertion order, deduplicated. The order the cart page lists them in. */
  ids = $state<string[]>(loadIds())
  /**
   * The chosen due date as an RFC 3339 instant, or null until the user picks
   * one. Held here so the dock can show it and a reload doesn't lose it.
   */
  dueAt = $state<string | null>(storage()?.getItem(DUE_KEY) ?? null)
  /**
   * Bumped on every add. The dock watches it to flash its background for
   * --dur-fast; it never navigates on its own, which would interrupt someone
   * mid-scan (design-system.md §8.4).
   */
  pulse = $state(0)

  get count() {
    return this.ids.length
  }

  get isEmpty() {
    return this.ids.length === 0
  }

  has(assetId: string) {
    return this.ids.includes(assetId)
  }

  /** Adds one asset. Idempotent, and returns false when it was already in. */
  add(assetId: string): boolean {
    if (this.has(assetId)) return false
    this.ids = [...this.ids, assetId]
    this.pulse += 1
    this.persist()
    return true
  }

  remove(assetId: string) {
    this.ids = this.ids.filter((id) => id !== assetId)
    this.persist()
  }

  /**
   * Drop several at once, for the `ErrConflict` path where the server names the
   * items that stopped being available and the dialog offers "remove them and
   * retry" (design-system.md §8.5).
   */
  removeMany(assetIds: string[]) {
    const drop = new Set(assetIds)
    this.ids = this.ids.filter((id) => !drop.has(id))
    this.persist()
  }

  setDueAt(iso: string | null) {
    this.dueAt = iso
    const store = storage()
    if (!store) return
    if (iso === null) store.removeItem(DUE_KEY)
    else store.setItem(DUE_KEY, iso)
  }

  clear() {
    this.ids = []
    this.setDueAt(null)
    storage()?.removeItem(STORAGE_KEY)
  }

  private persist() {
    storage()?.setItem(STORAGE_KEY, JSON.stringify(this.ids))
  }
}

export const cart = new CartStore()
