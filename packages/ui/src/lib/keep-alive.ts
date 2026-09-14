/**
 * Keeps a session alive while the person is actually using the app.
 *
 * The idle timeout is measured server-side from the **last request**
 * (`SessionStore.Get` stamps `LastSeen` on every resolve), not from sign-in.
 * That is the right anchor, but it leaves a gap: most of what someone does on
 * the browse screen costs no request at all. Expanding a model row, reading the
 * list, adding four units to the cart and picking a due date are all local
 * state. Ten minutes of that is ten minutes of silence as far as the server is
 * concerned, and the next press lands on a 401 that signs them out and empties
 * the cart — while they are standing there holding the camera.
 *
 * So: watch for real interaction, and turn it into a cheap `GET /me` when the
 * connection has been quiet too long. The effect is that the timeout measures
 * time since the last *interaction*, which is what it was always meant to mean.
 *
 * What this deliberately does **not** do is ping on a timer regardless of
 * activity. That would defeat the timeout entirely — the closet PC is shared and
 * unattended, and an abandoned session has to expire (CLAUDE.md §7).
 */

/** How often to consider pinging. Cheap: it only looks at two timestamps. */
export const KEEP_ALIVE_CHECK_MS = 30_000

/**
 * How quiet the connection must be before an interaction is worth a ping.
 *
 * Comfortably under the 10-minute default so a ping has room to fail and be
 * retried on the next tick, and long enough that a burst of clicking produces
 * one request rather than one per click.
 */
export const KEEP_ALIVE_AFTER_MS = 60_000

/**
 * Events that count as "someone is here".
 *
 * Deliberately not `mousemove` or `scroll`: a cat on the keyboard aside, those
 * fire from inertia and from a hand brushing the desk, and the point of the
 * timeout is that an *abandoned* machine locks itself. A press or a keystroke is
 * a person deciding something.
 */
const INTERACTION_EVENTS = ["pointerdown", "keydown"] as const

export interface KeepAliveOptions {
  /** False when nobody is signed in; the watcher idles without pinging. */
  isSignedIn: () => boolean
  /** When the API client last completed a request, in `performance.now()` terms. */
  lastRequestAt: () => number
  /** The ping itself. `GET /me` is the cheapest authenticated read there is. */
  ping: () => Promise<unknown>
  /** Injectable for tests. */
  now?: () => number
  checkMs?: number
  afterMs?: number
}

/**
 * Start watching. Returns a teardown that removes the listeners and stops the
 * timer, so it composes with Svelte's `$effect`/`onMount` return value.
 */
export function attachKeepAlive(options: KeepAliveOptions): () => void {
  const {
    isSignedIn,
    lastRequestAt,
    ping,
    now = () => performance.now(),
    checkMs = KEEP_ALIVE_CHECK_MS,
    afterMs = KEEP_ALIVE_AFTER_MS,
  } = options

  if (typeof window === "undefined") return () => {}

  let lastInteractionAt = now()
  /**
   * The interaction a ping has already been sent for.
   *
   * Without this the watcher re-pings every tick for the same press, and — worse
   * — a machine nobody has touched since it was attached pings forever, because
   * "no interaction yet" and "an interaction exactly as old as the last request"
   * compare the same way. That is the abandoned-closet-PC case the timeout
   * exists for, so it is tracked explicitly rather than inferred.
   */
  let handledInteractionAt = lastInteractionAt
  let pinging = false

  const noteInteraction = () => {
    lastInteractionAt = now()
  }
  for (const event of INTERACTION_EVENTS) {
    window.addEventListener(event, noteInteraction, { passive: true, capture: true })
  }

  const tick = async () => {
    if (pinging || !isSignedIn()) return
    const quietFor = now() - lastRequestAt()
    if (quietFor < afterMs) return
    // Only for an interaction we have not already acted on. An abandoned
    // machine never advances lastInteractionAt, so it never pings, and its
    // session expires exactly as intended.
    if (lastInteractionAt <= handledInteractionAt) return
    // Read once: an interaction landing mid-flight must not be marked handled
    // by a ping that started before it.
    const attemptedFor = lastInteractionAt
    pinging = true
    try {
      await ping()
      // Only a ping the server actually answered counts as handled. Advancing
      // this before the await meant a failed ping was never retried unless the
      // user happened to touch the machine again — the opposite of what the
      // "another go on the next tick" below promised.
      handledInteractionAt = attemptedFor
    } catch {
      // If it was a 401 the API client's own hook has already signed the user
      // out. Anything else gets another go on the next tick, because
      // handledInteractionAt is still where it was.
    } finally {
      pinging = false
    }
  }

  const timer = setInterval(tick, checkMs)

  return () => {
    clearInterval(timer)
    for (const event of INTERACTION_EVENTS) {
      window.removeEventListener(event, noteInteraction, { capture: true })
    }
  }
}
