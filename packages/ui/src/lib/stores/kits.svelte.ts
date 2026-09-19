/**
 * The kits screen's data: every kit with its units.
 *
 * One list for everybody. A student reads it to put a kit in the cart and an
 * admin edits it in place, which is why this is not an admin-panel store: the
 * two would otherwise be two lists of the same rows, and the admin's would be
 * the one that went stale (CLAUDE.md §7 — reading a kit is any full session).
 *
 * Every write endpoint answers with the whole kit, so an edit patches one row
 * here instead of refetching the list. That matters more than it looks: the
 * kits screen is where somebody adds six units to a kit one press at a time,
 * and a full reload per press would move the row under their cursor.
 */

import * as api from "../api/index"
import type { KitDetail } from "../api/types"

class KitsStore {
  /** By name, as the server sorts them. Nothing here re-sorts. */
  kits = $state<KitDetail[]>([])
  loading = $state(false)
  error = $state<string | null>(null)
  /** True once a load finished, so the empty state doesn't flash first. */
  loaded = $state(false)

  async reload() {
    this.loading = true
    this.error = null
    try {
      this.kits = await api.listKits()
      this.loaded = true
    } catch (error) {
      this.error = error instanceof Error ? error.message : String(error)
      this.kits = []
    } finally {
      this.loading = false
    }
  }

  /** Replace one kit in place, after an edit or a return. Adds it if new. */
  patch(next: KitDetail) {
    const at = this.kits.findIndex((k) => k.id === next.id)
    if (at === -1) {
      // A kit created on this screen: insert by name rather than appending, so
      // the list stays in the order the server would have returned.
      const before = this.kits.findIndex(
        (k) => k.name.localeCompare(next.name, undefined, { sensitivity: "base" }) > 0
      )
      const at = before === -1 ? this.kits.length : before
      this.kits = [...this.kits.slice(0, at), next, ...this.kits.slice(at)]
      return
    }
    this.kits = [...this.kits.slice(0, at), next, ...this.kits.slice(at + 1)]
  }

  remove(id: string) {
    this.kits = this.kits.filter((k) => k.id !== id)
  }

  reset() {
    this.kits = []
    this.loaded = false
    this.error = null
  }
}

export const kits = new KitsStore()
