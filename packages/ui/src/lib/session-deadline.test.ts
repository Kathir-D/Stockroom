import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { DEADLINE_MARGIN_MS, attachSessionDeadline } from "./session-deadline"

describe("attachSessionDeadline", () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  function setup(lastRequest: number, signedIn = true) {
    let clock = 0
    const probe = vi.fn(async () => {})
    const stop = attachSessionDeadline({
      isSignedIn: () => signedIn,
      lastRequestAt: () => lastRequest,
      idleMs: () => 60_000,
      probe,
      now: () => clock,
      checkMs: 1_000,
    })
    return {
      probe,
      stop,
      advance(ms: number) {
        clock += ms
        vi.advanceTimersByTime(ms)
      },
    }
  }

  it("does not ask while the session is still inside its idle length", () => {
    const t = setup(0)
    t.advance(60_000)
    expect(t.probe).not.toHaveBeenCalled()
    t.stop()
  })

  it("asks the server once the idle length and the margin have passed", () => {
    const t = setup(0)
    t.advance(60_000 + DEADLINE_MARGIN_MS + 1_000)
    expect(t.probe).toHaveBeenCalled()
    t.stop()
  })

  it("never asks with nobody signed in", () => {
    const t = setup(0, false)
    t.advance(10 * 60_000)
    expect(t.probe).not.toHaveBeenCalled()
    t.stop()
  })
})
