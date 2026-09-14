/**
 * Whether pressing **Add** opens the detail dialog first, or puts the unit
 * straight in the cart.
 *
 * `CLAUDE.md` §1 step 3 describes the flow as "click an item → detail popup →
 * Add to Cart": the popup is where someone confirms they are taking *this* body
 * rather than one that merely shares its model name, and it is the only place
 * the condition note, the photo and the custody history are visible.
 *
 * This module is deliberately the whole of that behaviour — one flag and one
 * predicate — because it is provisional. Setting `ADD_OPENS_DETAIL` to false
 * restores the direct add everywhere, with no other edit; deleting the feature
 * means deleting this file and the two call sites that import it.
 */

import type { AssetListItem } from "./api/types"

/**
 * The toggle. `true` routes Add through the detail dialog for units that have
 * something to show; `false` adds directly, as it did before 2026-09-14.
 */
export const ADD_OPENS_DETAIL = true

/**
 * A serial we generated rather than one a manufacturer stamped — the same shape
 * `<Serial>` shortens to a bare unit number. Kept in step with that component by
 * hand, which is fine for two call sites and wrong the moment there is a third.
 */
const MODEL_PREFIXED = /^[A-Za-z][\w.]*-0*\d+$/

/**
 * Whether the dialog would tell the viewer anything the row didn't.
 *
 * Linear items — batteries, bags, spare tripods, anything whose serial we
 * generated because the manufacturer stamped none — are interchangeable by
 * design. The row already carries the model, the unit number and the status, so
 * a popup for those is a press that buys nothing, which is exactly the kind of
 * ceremony `design-system.md` §1.1 says to cut. A photo or a condition note is
 * genuinely extra, so a linear item carrying either still opens.
 */
export function hasAddDetail(unit: AssetListItem): boolean {
  if (unit.photo_url) return true
  if (unit.condition?.trim()) return true
  if (unit.description?.trim() && !unit.serial_number) return true
  return !unit.serial_number || !MODEL_PREFIXED.test(unit.serial_number)
}

/** True when this press should open the dialog rather than add straight away. */
export function addShouldOpenDetail(unit: AssetListItem): boolean {
  return ADD_OPENS_DETAIL && hasAddDetail(unit)
}
