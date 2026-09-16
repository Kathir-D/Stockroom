import {afterEach, describe, expect, it, vi} from 'vitest'

import type {AssetCustody} from './api/types'
import {abbreviateName, resolveStatus} from './status'

/**
 * The viewer-relative half of the status system (status.ts, design-system.md
 * §6): an item that is out is blue when the person looking at it is holding it
 * and orange when somebody else is.
 *
 * Worth pinning because the failure is silent and backwards-compatible: pass no
 * viewer and every row still renders, just in the wrong colour with the wrong
 * words, and nothing in the type system or the build notices. The whole point of
 * the split is that a student on a shared closet machine can tell their own
 * items apart at a glance, so "shows orange for an item that is actually yours"
 * is the exact bug these tests exist to catch.
 */

const ME = 'viewer-1'
const THEM = 'viewer-2'

/** `now` is frozen in each test, so due dates are written relative to it. */
const NOW = new Date('2026-09-16T12:00:00Z')
const in3Days = new Date(NOW.getTime() + 3 * 24 * 60 * 60 * 1000).toISOString()
const in12Hours = new Date(NOW.getTime() + 12 * 60 * 60 * 1000).toISOString()
const twoDaysAgo = new Date(NOW.getTime() - 2 * 24 * 60 * 60 * 1000).toISOString()

function custody(over: Partial<AssetCustody> = {}): AssetCustody {
  return {
    custody_event_id: 'event-1',
    custodian_id: THEM,
    custodian_name: 'Jordan Smith',
    student_number: null,
    checked_out_at: NOW.toISOString(),
    due_at: in3Days,
    overdue: false,
    ...over
  }
}

const out = (over: Partial<AssetCustody> = {}) =>
  ({status: 'checked_out', custody: custody(over)}) as const

describe('abbreviateName', () => {
  it('reduces a surname to an initial', () => {
    expect(abbreviateName('Jordan Smith')).toBe('Jordan S')
  })

  it('leaves a mononym alone', () => {
    // displayName() falls back to the student number for a profile with no name
    // at all, and "123456" must not come out as "1."
    expect(abbreviateName('123456')).toBe('123456')
    expect(abbreviateName('Prince')).toBe('Prince')
  })

  it('takes the initial from the last token, keeping the rest', () => {
    expect(abbreviateName('Mary Jane Watson')).toBe('Mary Jane W')
  })

  it('strips a period from a surname already entered as an initial', () => {
    // A roster row spelled "Jordan S." must render the same as "Jordan Smith".
    expect(abbreviateName('Jordan S.')).toBe('Jordan S')
  })
})

describe('resolveStatus', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  const freeze = () => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
  }

  it('is blue and unattributed for an item the viewer holds', () => {
    freeze()
    const status = resolveStatus(out({custodian_id: ME}), ME)
    expect(status.state).toBe('out')
    expect(status.fg).toBe('text-status-out')
    expect(status.label).toMatch(/^Checked out · due /)
    // Never "by you": the absence of a name is what says it is yours.
    expect(status.label).not.toContain('by')
  })

  it('is orange and names the holder for somebody else’s item', () => {
    freeze()
    const status = resolveStatus(out(), ME)
    expect(status.state).toBe('out-other')
    expect(status.fg).toBe('text-status-out-other')
    expect(status.label).toMatch(/^Checked out by Jordan S · due /)
  })

  it('treats an unknown viewer as somebody else', () => {
    // Signed out, or a caller with no viewer in scope. Getting this wrong the
    // other way would paint a stranger's item as the viewer's own.
    freeze()
    expect(resolveStatus(out()).state).toBe('out-other')
    expect(resolveStatus(out({custodian_id: ME}), null).state).toBe('out-other')
  })

  it('keeps overdue red for either holder, naming only the other one', () => {
    freeze()
    const theirs = resolveStatus(out({due_at: twoDaysAgo, overdue: true}), ME)
    expect(theirs.state).toBe('overdue')
    expect(theirs.label).toBe('Overdue 2 days · Jordan S')

    const mine = resolveStatus(out({custodian_id: ME, due_at: twoDaysAgo, overdue: true}), ME)
    expect(mine.state).toBe('overdue')
    expect(mine.label).toBe('Overdue 2 days')
  })

  it('keeps due-soon amber for either holder, naming only the other one', () => {
    freeze()
    const theirs = resolveStatus(out({due_at: in12Hours}), ME)
    expect(theirs.state).toBe('due-soon')
    expect(theirs.label).toMatch(/· Jordan S$/)

    const mine = resolveStatus(out({custodian_id: ME, due_at: in12Hours}), ME)
    expect(mine.state).toBe('due-soon')
    expect(mine.label).not.toContain('Jordan')
  })

  it('still names the holder when there is no due date', () => {
    freeze()
    expect(resolveStatus(out({due_at: null}), ME).label).toBe('Checked out by Jordan S')
    expect(resolveStatus(out({custodian_id: ME, due_at: null}), ME).label).toBe('Checked out')
  })

  it('ignores the viewer for an item nobody holds', () => {
    expect(resolveStatus({status: 'available', custody: null}, ME).state).toBe('available')
    expect(resolveStatus({status: 'unavailable', custody: null}, ME).state).toBe('unavailable')
    // checked_out with no open custody row is status drift, and stays the honest
    // grey it was — there is no holder to attribute it to.
    expect(resolveStatus({status: 'checked_out', custody: null}, ME).state).toBe('unavailable')
  })
})
