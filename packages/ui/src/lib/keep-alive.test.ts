import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'

import {attachKeepAlive} from './keep-alive'

/**
 * The first tests in `packages/ui`. This module gets them because it is the one
 * piece of the idle-timeout behaviour with a real decision in it: ping when the
 * person is here, stay quiet when the machine has been abandoned. Getting that
 * backwards in either direction is invisible until someone loses a cart, or
 * until an unattended closet PC never locks.
 */
describe('attachKeepAlive', () => {
  let clock = 0
  let lastRequest = 0
  // Typed as the option it is passed as, so svelte-check sees the same shape
  // attachKeepAlive asks for rather than vi.fn's catch-all signature.
  let ping: (() => Promise<unknown>) & {mock: {calls: unknown[]}}

  const start = (isSignedIn = () => true) =>
    attachKeepAlive({
      isSignedIn,
      lastRequestAt: () => lastRequest,
      ping,
      now: () => clock,
      checkMs: 1_000,
      afterMs: 60_000
    })

  /** Move time forward and let the interval fire, awaiting any ping it starts. */
  const advance = async (ms: number) => {
    clock += ms
    await vi.advanceTimersByTimeAsync(ms)
  }

  const interact = () => window.dispatchEvent(new KeyboardEvent('keydown', {key: 'a'}))

  beforeEach(() => {
    vi.useFakeTimers()
    clock = 0
    lastRequest = 0
    // The real ping is a request, so it refreshes the same clock the API
    // client stamps. Mirroring that here keeps the fake honest.
    ping = vi.fn(async () => {
      lastRequest = clock
    }) as typeof ping
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('pings when someone has interacted and the connection has gone quiet', async () => {
    const stop = start()
    await advance(90_000)
    interact()
    await advance(1_000)
    expect(ping).toHaveBeenCalledTimes(1)
    stop()
  })

  // The whole point of the timeout: a shared machine nobody is using has to
  // expire. A keep-alive that ran on a bare timer would defeat it.
  it('stays silent when nobody has interacted, however long it waits', async () => {
    const stop = start()
    await advance(600_000)
    expect(ping).not.toHaveBeenCalled()
    stop()
  })

  it('stays silent while requests are already flowing', async () => {
    const stop = start()
    interact()
    // A request just happened, so the server's deadline is fresh regardless.
    lastRequest = clock
    await advance(30_000)
    expect(ping).not.toHaveBeenCalled()
    stop()
  })

  it('does nothing when nobody is signed in', async () => {
    const stop = start(() => false)
    await advance(90_000)
    interact()
    await advance(1_000)
    expect(ping).not.toHaveBeenCalled()
    stop()
  })

  it('does not stack pings when one is still in flight', async () => {
    let release: () => void = () => {}
    ping = vi.fn(() => new Promise<void>((resolve) => (release = resolve))) as typeof ping
    const stop = start()
    await advance(90_000)
    interact()
    await advance(5_000)
    expect(ping).toHaveBeenCalledTimes(1)
    release()
    stop()
  })

  it('stops listening after teardown', async () => {
    const stop = start()
    stop()
    await advance(90_000)
    interact()
    await advance(1_000)
    expect(ping).not.toHaveBeenCalled()
  })
})
