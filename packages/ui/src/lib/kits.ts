/**
 * Putting a kit in the cart.
 *
 * A kit is a label over assets and never holds custody itself, so "add the kit"
 * is "add its units" — `CheckOutAssets` then commits them as an ordinary cart
 * (CLAUDE.md §2, TODO Phase 8). That expansion happens here, in one pure
 * function, rather than inside the screen, because it is the only part of the
 * feature with a rule in it and the only part worth a test.
 *
 * The three buckets exist because the kit on screen can be out of date. The
 * **Add** button is disabled unless the server called the kit `checkable`, but
 * that flag was true when the list was fetched, and on a shared closet machine
 * somebody else can take a unit in between. Re-deciding per unit at press time
 * means the cart never quietly contains an item that is already in a bag, and
 * the screen can say which unit went missing instead of failing later inside
 * the checkout transaction (design-system.md §8.4).
 */

import type { AssetListItem, KitDetail } from "./api/types"
import { resolveStatus } from "./status"

export interface KitCartPlan {
  /** Units to add now: available, and not already in the cart. */
  addable: AssetListItem[]
  /** Already in the cart. Not an error — a cart is a set (CLAUDE.md §13). */
  alreadyInCart: AssetListItem[]
  /** Out or out of service, so the kit cannot go out whole right now. */
  blocked: AssetListItem[]
}

/**
 * Split a kit's units into what a press should do with them.
 *
 * Availability is `resolveStatus`, not `unit.status`, for the same reason
 * `<UnitRow>` uses it: a `checked_out` row with no open custody event is status
 * drift, and the one place that decides what state a unit is in is `status.ts`.
 */
export function kitCartPlan(kit: KitDetail, cartIds: readonly string[]): KitCartPlan {
  const inCart = new Set(cartIds)
  const plan: KitCartPlan = { addable: [], alreadyInCart: [], blocked: [] }
  for (const unit of kit.items) {
    if (resolveStatus(unit).state !== "available") plan.blocked.push(unit)
    else if (inCart.has(unit.id)) plan.alreadyInCart.push(unit)
    else plan.addable.push(unit)
  }
  return plan
}

/**
 * Whether the press should go ahead.
 *
 * All or nothing, deliberately: half a kit is a camera with no lens, and the
 * person carrying it finds out at the shoot. An empty kit is refused too —
 * "check out nothing" is a press that does nothing.
 */
export function kitIsAddable(plan: KitCartPlan): boolean {
  if (plan.blocked.length > 0) return false
  return plan.addable.length + plan.alreadyInCart.length > 0
}

/**
 * The sentence to show after a press, or instead of one.
 *
 * Worded here beside the rule that produced it so the two cannot drift, and
 * returned as one string because every caller shows it in a toast.
 */
export function kitPlanMessage(kit: KitDetail, plan: KitCartPlan): string {
  if (plan.blocked.length > 0) {
    const names = plan.blocked.map((u) => u.name)
    const listed = names.slice(0, 2).join(", ")
    const rest = names.length > 2 ? ` and ${names.length - 2} more` : ""
    return `${kit.name} isn't complete: ${listed}${rest} ${names.length === 1 ? "is" : "are"} not on the shelf.`
  }
  if (plan.addable.length === 0 && plan.alreadyInCart.length === 0) {
    return `${kit.name} has no items in it yet.`
  }
  if (plan.addable.length === 0) {
    return `${kit.name} is already in the cart.`
  }
  const added = `${plan.addable.length} item${plan.addable.length === 1 ? "" : "s"}`
  if (plan.alreadyInCart.length > 0) {
    return `Added ${added} from ${kit.name}; ${plan.alreadyInCart.length} already in the cart.`
  }
  return `Added ${added} from ${kit.name}.`
}
