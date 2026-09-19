import {describe, expect, it} from 'vitest'

import type {AssetCustody, AssetListItem, KitDetail} from './api/types'
import {kitCartPlan, kitIsAddable, kitPlanMessage} from './kits'

/**
 * Expanding a kit into cart lines (kits.ts).
 *
 * The rule worth pinning is the one the screen cannot show: a kit's **Add**
 * button is enabled from `checkable`, which was true when the list was fetched,
 * so the press has to re-decide per unit or the cart silently accepts an item
 * somebody else is already carrying. The failure is invisible until checkout
 * fails with a 409 naming an item the person never chose.
 */

function unit(over: Partial<AssetListItem> = {}): AssetListItem {
  return {
    id: 'asset-1',
    asset_tag: 'AST-000001',
    name: 'Canon T7i',
    description: null,
    category_id: null,
    location_id: null,
    status: 'available',
    condition: null,
    serial_number: 'T7i-001',
    purchase_date: null,
    purchase_price: null,
    warranty_expiration: null,
    custom_fields: null,
    photo_path: null,
    created_by: null,
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    category_path: [],
    photo_url: null,
    custody: null,
    ...over
  }
}

function held(): AssetCustody {
  return {
    custody_event_id: 'event-1',
    custodian_id: 'someone-else',
    custodian_name: 'Jordan Smith',
    student_number: null,
    checked_out_at: '2026-09-16T12:00:00Z',
    due_at: '2026-09-20T12:00:00Z',
    overdue: false
  }
}

function kit(items: AssetListItem[], over: Partial<KitDetail> = {}): KitDetail {
  return {
    id: 'kit-1',
    name: 'Kit #1',
    description: null,
    created_at: '2026-09-01T00:00:00Z',
    items,
    available: items.length,
    checked_out: 0,
    unavailable: 0,
    checkable: true,
    ...over
  }
}

describe('kitCartPlan', () => {
  it('adds every available unit that is not already in the cart', () => {
    const a = unit({id: 'a'})
    const b = unit({id: 'b', name: 'Canon 24-70mm'})
    const plan = kitCartPlan(kit([a, b]), ['b'])

    expect(plan.addable.map((u) => u.id)).toEqual(['a'])
    expect(plan.alreadyInCart.map((u) => u.id)).toEqual(['b'])
    expect(plan.blocked).toEqual([])
    expect(kitIsAddable(plan)).toBe(true)
    expect(kitPlanMessage(kit([a, b]), plan)).toBe(
      'Added 1 item from Kit #1; 1 already in the cart.'
    )
  })

  it('refuses the whole kit when a unit is out, naming it', () => {
    const a = unit({id: 'a'})
    const b = unit({id: 'b', name: 'Canon 24-70mm', status: 'checked_out', custody: held()})
    const k = kit([a, b], {available: 1, checked_out: 1, checkable: false})
    const plan = kitCartPlan(k, [])

    expect(plan.blocked.map((u) => u.id)).toEqual(['b'])
    expect(kitIsAddable(plan)).toBe(false)
    expect(kitPlanMessage(k, plan)).toBe(
      "Kit #1 isn't complete: Canon 24-70mm is not on the shelf."
    )
  })

  /**
   * Status drift: `checked_out` with no open custody row. `status.ts` is the one
   * place that decides what state a unit is in, and it reads that as
   * unavailable — a plan that tested `unit.status === 'available'` itself would
   * agree here but disagree on the row the server reports as held.
   */
  it('treats an unavailable unit as blocking, whatever the column says', () => {
    const k = kit([unit({status: 'unavailable'})], {available: 0, unavailable: 1, checkable: false})
    expect(kitIsAddable(kitCartPlan(k, []))).toBe(false)
  })

  it('will not add an empty kit', () => {
    const k = kit([], {available: 0, checkable: false})
    const plan = kitCartPlan(k, [])
    expect(kitIsAddable(plan)).toBe(false)
    expect(kitPlanMessage(k, plan)).toBe('Kit #1 has no items in it yet.')
  })

  it('says so when every unit is already in the cart', () => {
    const k = kit([unit({id: 'a'})])
    expect(kitPlanMessage(k, kitCartPlan(k, ['a']))).toBe('Kit #1 is already in the cart.')
  })
})
