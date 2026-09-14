/**
 * Resolves the cart's asset ids to full rows, and keeps them live.
 *
 * The cart itself stores only ids, because it survives a page reload through
 * sessionStorage and a cached asset row would go stale the moment someone else
 * touched the item. The cart page needs a **live** status per line, so that an
 * item somebody else took while the cart sat idle turns red there instead of
 * failing inside the checkout transaction (design-system.md §8.4).
 *
 * `GET /assets/{id}` and `GET /assets` return the same struct, so a row already
 * on screen can be reused and only the unknown ids cost a request.
 */

import * as api from "../api/index"
import type { AssetListItem } from "../api/types"
import { cart } from "./cart.svelte"

class CartItemsStore {
  /** In cart order. Ids that no longer resolve are dropped, not left blank. */
  items = $state<AssetListItem[]>([])
  loading = $state(false)
  error = $state<string | null>(null)

  /**
   * Refetch every line. Called when the cart page opens and after a failed
   * checkout, which are the two moments the statuses have to be trustworthy.
   */
  async refresh(seed: AssetListItem[] = []) {
    const ids = cart.ids
    if (ids.length === 0) {
      this.items = []
      return
    }
    this.loading = true
    this.error = null
    const known = new Map(seed.map((a) => [a.id, a]))
    try {
      const resolved = await Promise.all(
        ids.map(async (id) => {
          try {
            // Always refetch: a seeded row is only a fallback for a 404.
            return await api.getAsset(id)
          } catch {
            return known.get(id) ?? null
          }
        })
      )
      const found = resolved.filter((a): a is AssetListItem => a !== null)
      // An id that resolves to nothing has been deleted out from under the cart.
      // Dropping it here keeps the count in the dock honest.
      const missing = ids.filter((id) => !found.some((a) => a.id === id))
      if (missing.length > 0) cart.removeMany(missing)
      this.items = found
    } catch (error) {
      this.error = error instanceof Error ? error.message : String(error)
    } finally {
      this.loading = false
    }
  }

  /** The ids that are no longer available, for the §8.5 conflict dialog. */
  get unavailableIds() {
    return this.items.filter((a) => a.status !== "available").map((a) => a.id)
  }

  clear() {
    this.items = []
    this.error = null
  }
}

export const cartItems = new CartItemsStore()
