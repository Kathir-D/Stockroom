/**
 * The browse screen's data: the category tree, the current filter, the units
 * that match, and the model groups the list draws.
 *
 * The server already returns units in browse order — category position in the
 * tree, then available before checked_out before unavailable, then name, then
 * asset tag (CLAUDE.md §13, 2026-09-13). Nothing here re-sorts them. Grouping
 * preserves first-appearance order, so `examples/categories.media-department.md`'s document order survives
 * into the rendered list without this file knowing what that order is.
 */

import * as api from "../api/index"
import type { AssetListItem, AssetStatus, CategoryNode } from "../api/types"

/**
 * One row of the browse list: a model, with its units behind an accordion.
 *
 * The availability count and the unit a model-level **Add** points at are both
 * derived here rather than returned by the server, because `ListAssets` already
 * carries every unit's status and holder — deriving them keeps `CheckOutAssets`
 * unchanged (design-system.md §8.2, "Backend, as built").
 */
export interface ModelGroup {
  key: string
  name: string
  /** Root-first path of the deepest category node the units file under. */
  categoryPath: { id: string; name: string }[]
  units: AssetListItem[]
  availableCount: number
  outCount: number
  unavailableCount: number
  /** First unit with a photo, so the row has a thumbnail even if one lacks it. */
  thumbnail: string | null
}

/**
 * Group units into model rows.
 *
 * Keyed on category node *and* name, not category alone: an asset may file under
 * any node in the tree, not only a Model (docs/adr/0001), so two differently
 * named things can share a parent and must not collapse into one row.
 */
export function groupByModel(units: AssetListItem[]): ModelGroup[] {
  const groups = new Map<string, ModelGroup>()
  for (const unit of units) {
    const key = `${unit.category_id ?? "-"}::${unit.name}`
    let group = groups.get(key)
    if (!group) {
      group = {
        key,
        name: unit.name,
        categoryPath: unit.category_path ?? [],
        units: [],
        availableCount: 0,
        outCount: 0,
        unavailableCount: 0,
        thumbnail: null,
      }
      groups.set(key, group)
    }
    group.units.push(unit)
    if (unit.status === "available") group.availableCount += 1
    else if (unit.status === "checked_out") group.outCount += 1
    else group.unavailableCount += 1
    if (!group.thumbnail && unit.photo_url) group.thumbnail = unit.photo_url
  }
  return [...groups.values()]
}

/** Flatten the tree to a lookup, for breadcrumbs and the "no models yet" state. */
function indexTree(tree: CategoryNode[]): Map<string, CategoryNode> {
  const index = new Map<string, CategoryNode>()
  const walk = (nodes: CategoryNode[]) => {
    for (const node of nodes) {
      index.set(node.id, node)
      walk(node.children)
    }
  }
  walk(tree)
  return index
}

class CatalogStore {
  tree = $state<CategoryNode[]>([])
  units = $state<AssetListItem[]>([])

  /** The selected category node, or undefined for the whole inventory. */
  category = $state<string | undefined>(undefined)
  status = $state<AssetStatus | undefined>(undefined)
  search = $state("")

  loading = $state(false)
  error = $state<string | null>(null)

  /** True once a load has completed, so the empty state doesn't flash first. */
  loaded = $state(false)

  private index = $derived(indexTree(this.tree))
  private inflight: AbortController | null = null

  groups = $derived(groupByModel(this.units))

  /** The node the sidebar has selected, for the top-bar breadcrumb. */
  get selectedNode() {
    return this.category ? this.index.get(this.category) : undefined
  }

  /**
   * Root-first path to the selected node, for the breadcrumb. Walks the tree
   * rather than storing parents, because the tree is a few dozen rows.
   */
  get selectedPath(): CategoryNode[] {
    if (!this.category) return []
    const path: CategoryNode[] = []
    const walk = (nodes: CategoryNode[], trail: CategoryNode[]): boolean => {
      for (const node of nodes) {
        const next = [...trail, node]
        if (node.id === this.category) {
          path.push(...next)
          return true
        }
        if (walk(node.children, next)) return true
      }
      return false
    }
    walk(this.tree, [])
    return path
  }

  /**
   * True when the selected category genuinely holds no units, as opposed to the
   * filters matching nothing. `Primes` is seeded empty because `examples/categories.media-department.md`
   * records no primes in inventory, and that needs a different message from "no
   * results" (design-system.md §8.2).
   */
  get selectedIsEmptyBranch() {
    const node = this.selectedNode
    if (!node) return false
    return node.children.length === 0 && !this.search && !this.status
  }

  async loadTree() {
    this.tree = await api.categoryTree()
  }

  /**
   * Reload the unit list for the current filter.
   *
   * Search hits the server, which has a GIN index for it and also matches
   * partial identifiers `T7iB` -> `T7iBat-001` that a tsquery can't. An
   * in-flight request is aborted first, so fast typing can't land out of order.
   */
  async reload() {
    this.inflight?.abort()
    const controller = new AbortController()
    this.inflight = controller
    this.loading = true
    this.error = null
    try {
      this.units = await api.listAssets(
        { category: this.category, status: this.status, q: this.search.trim() || undefined },
        controller.signal
      )
      this.loaded = true
    } catch (error) {
      if (controller.signal.aborted) return
      this.error = error instanceof Error ? error.message : String(error)
      this.units = []
    } finally {
      if (this.inflight === controller) {
        this.inflight = null
        this.loading = false
      }
    }
  }

  /**
   * The unit a scan just landed on. The browse list opens that unit's group and
   * highlights the row for --dur-slow, so someone who scanned an item and then
   * dismissed the surface can see where it lives (design-system.md §8.2).
   */
  highlightedUnitId = $state<string | null>(null)
  private highlightTimer: ReturnType<typeof setTimeout> | null = null

  highlightUnit(id: string) {
    this.highlightedUnitId = id
    if (this.highlightTimer) clearTimeout(this.highlightTimer)
    // --dur-slow, given a little room to be seen after the surface closes.
    this.highlightTimer = setTimeout(() => (this.highlightedUnitId = null), 1600)
  }

  /** Replace one unit in place, after a check-in or a status change. */
  patchUnit(next: AssetListItem) {
    const at = this.units.findIndex((u) => u.id === next.id)
    if (at === -1) return
    this.units = [...this.units.slice(0, at), next, ...this.units.slice(at + 1)]
  }

  reset() {
    this.tree = []
    this.units = []
    this.category = undefined
    this.status = undefined
    this.search = ""
    this.loaded = false
    this.error = null
  }
}

export const catalog = new CatalogStore()
