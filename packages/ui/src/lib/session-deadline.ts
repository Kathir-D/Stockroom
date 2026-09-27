/**
 * Returns the screen to sign-in once the server has dropped the session.
 *
 * The server ends a session after `SESSION_IDLE_MINUTES` without a request,
 * but the UI only finds out on its next request, and `keep-alive.ts` only
 * sends one after somebody touches the machine. Until then the last person's
 * name, cart and browse list stay on screen for whoever walks up next
 * (CLAUDE.md §7).
 *
 * So this watches the same clock the server does, the time of the last
 * request, and once it is past the idle length by a margin it asks the
 * server. It asks rather than signing out locally, because only the server
 * knows the deadline for certain: the answer is a 401, and the API client's
 * 401 hook does the rest. The margin makes sure the question cannot itself
 * be the request that keeps an almost-expired session alive.
 */

/** How often to look at the clock. */
export const DEADLINE_CHECK_MS = 5_000

/** How far past the idle length to wait before asking. */
export const DEADLINE_MARGIN_MS = 3_000

export interface SessionDeadlineOptions {
  isSignedIn: () => boolean
  /** When the last authenticated request completed, in `performance.now()` terms. */
  lastRequestAt: () => number
  /** The server's idle length in ms; zero or less means sessions never expire. */
  idleMs: () => number
  /** Ask the server; a 401 answer signs the UI out. */
  probe: () => Promise<unknown>
  now?: () => number
  checkMs?: number
}

export function attachSessionDeadline(options: SessionDeadlineOptions): () => void {
  const { isSignedIn, lastRequestAt, idleMs, probe, now = () => performance.now(), checkMs = DEADLINE_CHECK_MS } = options
  if (typeof window === "undefined") return () => {}

  let probing = false
  const tick = async () => {
    const idle = idleMs()
    if (probing || idle <= 0 || !isSignedIn()) return
    if (now() - lastRequestAt() < idle + DEADLINE_MARGIN_MS) return
    probing = true
    try {
      await probe()
    } catch {
      // A 401 has already been handled by the API client's hook. Anything
      // else (the server is down) gets another look on the next tick.
    } finally {
      probing = false
    }
  }

  const timer = setInterval(() => void tick(), checkMs)
  return () => clearInterval(timer)
}
