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
import { ApiError } from "../api/client"
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
          } catch (error) {
            // **Only a 404 means the asset is gone.** A timeout, a 500 or the
            // server being down says nothing about whether the item still
            // exists, and treating those the same way emptied the cart of
            // everything it could not reach. Anything else rethrows into the
            // outer catch, which surfaces the error and leaves the cart alone.
            if (error instanceof ApiError && error.status === 404) return null
            throw error
          }
        })
      )
      const found = resolved.filter((a): a is AssetListItem => a !== null)
      // An id that 404s has been deleted out from under the cart. Dropping it
      // here keeps the count in the dock honest.
      const missing = ids.filter((id) => !found.some((a) => a.id === id))
      if (missing.length > 0) cart.removeMany(missing)
      this.items = found
    } catch (error) {
      // The cart's ids are untouched, so a retry once the server is back
      // resolves them all. Seeded rows keep the page rendering something.
      this.items = ids.map((id) => known.get(id)).filter((a): a is AssetListItem => a !== undefined)
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
