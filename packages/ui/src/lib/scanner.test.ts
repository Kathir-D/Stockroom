import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'

import {attachScanner, type ScanBurst} from './scanner'

/**
 * The scan-vs-typed split and, more importantly, what the buffer holds when a
 * person edits what they typed.
 *
 * That second one is why this file exists. The buffer only ever grew: typing a
 * number, deleting it, and pressing Enter signed you in as the *deleted*
 * number, because Backspace was not a printable character and so was ignored.
 * On the sign-in screen that is a wrong-account login with no visible cause —
 * the box was empty, and somebody still got signed in.
 */
describe('attachScanner', () => {
  let clock = 0
  let bursts: ScanBurst[]
  let detach: () => void

  beforeEach(() => {
    clock = 0
    bursts = []
    vi.spyOn(performance, 'now').mockImplementation(() => clock)
    detach = attachScanner({
      captureInsideFields: true,
      onBurst: (burst) => bursts.push(burst),
    })
  })

  afterEach(() => {
    detach()
    vi.restoreAllMocks()
  })

  /** One keystroke, `gap` ms after the previous one. */
  const key = (k: string, gap = 200, modifiers: KeyboardEventInit = {}) => {
    clock += gap
    window.dispatchEvent(new KeyboardEvent('keydown', {key: k, bubbles: true, ...modifiers}))
  }

  const type = (text: string, gap = 200, modifiers: KeyboardEventInit = {}) => {
    for (const ch of text) key(ch, gap, modifiers)
  }

  /** A scanner burst: the whole code and its Enter, all inside the threshold. */
  const scan = (code: string) => {
    type(code, 5)
    key('Enter', 5)
  }

  it('reports a fast burst as a scan', () => {
    type('123456', 5)
    key('Enter', 5)

    expect(bursts).toHaveLength(1)
    expect(bursts[0].code).toBe('123456')
    expect(bursts[0].fast).toBe(true)
  })

  it('reports a slowly typed burst as typed', () => {
    type('123456')
    key('Enter')

    expect(bursts).toHaveLength(1)
    expect(bursts[0].code).toBe('123456')
    expect(bursts[0].fast).toBe(false)
  })

  it('drops backspaced characters instead of keeping them', () => {
    type('999')
    key('Backspace')
    key('Backspace')
    key('Backspace')
    type('123456')
    key('Enter')

    expect(bursts[0].code).toBe('123456')
  })

  it('reports nothing when the whole entry is backspaced away', () => {
    type('123456')
    for (let i = 0; i < 6; i++) key('Backspace')
    key('Enter')

    // An empty buffer means the Enter is not a burst at all, which is what lets
    // the sign-in form's native submit read the field instead.
    expect(bursts).toHaveLength(0)
  })

  it('treats an edited burst as typed even when the keystrokes were fast', () => {
    // Editing is something only a person does; a scanner sends the code and an
    // Enter. So a burst that was corrected is never a scan, however quick it
    // was -- and a scan is the branch that signs somebody in with no password.
    type('1234', 5)
    key('Backspace', 5)
    type('5', 5)
    key('Enter', 5)

    expect(bursts[0].code).toBe('1235')
    expect(bursts[0].fast).toBe(false)
  })

  it('clears the buffer on Escape', () => {
    type('999')
    key('Escape')
    type('123456')
    key('Enter')

    expect(bursts[0].code).toBe('123456')
  })

  /**
   * The five below are the second half of the same bug. Moving the buffer in
   * step with Backspace was not enough on its own: an idle Backspace poisoned
   * the *next* burst, and every editing gesture that is a chord or a caret move
   * still slipped past the buffer entirely.
   */
  it('does not let an idle Backspace demote the next scan to typed', () => {
    // Somebody taps Backspace at an empty field — a reflex, and it used to set
    // `fast = false` permanently, because only Enter and Escape cleared it.
    // The next card scan then asked for a password, which a roster-imported
    // user does not have yet (CLAUDE.md §7), so they could not get in at all.
    key('Backspace')
    scan('123456')

    expect(bursts[0].code).toBe('123456')
    expect(bursts[0].fast).toBe(true)
  })

  it('still reads a scan after the field is cleared one Backspace at a time', () => {
    type('999')
    for (let i = 0; i < 3; i++) key('Backspace')
    scan('123456')

    expect(bursts[0].fast).toBe(true)
  })

  it('discards the burst when an editing chord deletes text it cannot see', () => {
    // Alt+Backspace (delete word) and Cmd+Backspace (delete to line start)
    // empty the field in one keystroke. The chord guard used to `return`
    // outright, so the buffer kept "123456" *and* stayed fast — an empty box
    // that signed you in as the number you had just deleted, which is exactly
    // the bug the Backspace handling was added to fix.
    type('123456', 5)
    key('Backspace', 5, {altKey: true})
    key('Enter', 5)

    expect(bursts).toHaveLength(0)
  })

  it('drops the replaced text when select-all precedes an overtype', () => {
    type('123456', 5)
    key('a', 5, {metaKey: true})
    type('9', 5)
    key('Enter', 5)

    // The field now reads "9", and so does the buffer. Before the fix it read
    // "1234569" — a value that was never on screen and that the old `fast`
    // branch would have signed somebody in with.
    expect(bursts[0].code).toBe('9')
    expect(bursts[0].fast).toBe(false)
  })

  it('discards the burst when the caret moves', () => {
    // Arrow keys mean the next character lands somewhere the buffer is not
    // tracking, so the buffer stops describing the field.
    type('123456', 5)
    key('ArrowLeft', 5)
    type('9', 5)
    key('Enter', 5)

    expect(bursts[0].code).toBe('9')
    expect(bursts[0].fast).toBe(false)
  })
})
