/**
 * Keyboard-wedge barcode scanner support (CLAUDE.md §10, design-system.md §9).
 *
 * A USB barcode scanner is an HID keyboard: it types the code into whatever has
 * focus and ends with Enter. There is no SDK and no driver, so the only thing
 * that distinguishes a scan from someone typing is *speed*. This module buffers
 * keystrokes, measures the gaps, and reports each completed burst as either a
 * scan or a typed entry.
 *
 * Which endpoint a burst hits is decided by the active screen, never by the
 * payload: the sign-in screen posts to `/auth/scan`, everything else posts to
 * `/scan`. The backend never guesses (CLAUDE.md §10).
 */

/**
 * The maximum gap between two keystrokes for a burst to count as a scan.
 *
 * This is a guess until the hardware arrives in Week 7, which is exactly why it
 * is one named constant: tuning it against a real scanner is then a one-line
 * change rather than an archaeology exercise (CLAUDE.md §13, "still open").
 * `Ctrl+Shift+D` prints the last burst's inter-key timings to the console; use
 * it on the day the scanner shows up.
 */
export const SCAN_KEY_THRESHOLD_MS = 50

/**
 * The shortest burst that can be a scan. A single keystroke followed by Enter
 * has no gap to measure, so without a floor every stray Enter on a one-character
 * field would read as a scan.
 */
const MIN_SCAN_LENGTH = 2

/**
 * Keys that end a burst without telling us what is left in the field.
 *
 * A scanner sends the code and an Enter, nothing else, so every one of these is
 * a person: caret moves, Delete, Escape. Each can leave the field holding
 * something the buffer has no way to reconstruct, and a buffer that no longer
 * matches the field is worse than no buffer at all — it is the difference
 * between "nothing happened" and "somebody was signed in as the wrong student".
 *
 * Backspace is deliberately absent: it removes exactly one character, which the
 * buffer can follow.
 */
const DISCARDING_KEYS = new Set([
  "Delete",
  "Escape",
  "ArrowLeft",
  "ArrowRight",
  "ArrowUp",
  "ArrowDown",
  "Home",
  "End",
  "PageUp",
  "PageDown",
])

export interface ScanBurst {
  /** The characters typed before Enter, untrimmed. */
  code: string
  /** True when every gap in the burst was under the threshold. */
  fast: boolean
  /** Gaps in ms between consecutive keystrokes, for the diagnostic. */
  timings: number[]
  /** The element that had focus when Enter arrived. */
  target: EventTarget | null
}

export interface ScannerOptions {
  /** Called once per completed burst (Enter pressed with something buffered). */
  onBurst: (burst: ScanBurst) => void
  /**
   * When true, bursts are still reported while focus sits inside a text field.
   *
   * The default is false, and that matters: a keyboard-wedge scanner types into
   * the focused element, so without this guard typing a search query or a damage
   * note would fire phantom scans (design-system.md §9.3). The sign-in screen is
   * the deliberate exception, because its input *is* the scan target.
   */
  captureInsideFields?: boolean
  /** Override the threshold, for tests and for the Week 7 tuning session. */
  thresholdMs?: number
  /** Where to listen. Defaults to `window`. */
  target?: Window | HTMLElement
}

/** True when focus is somewhere that a person is deliberately typing into. */
function isTextEntry(node: EventTarget | null): boolean {
  if (!(node instanceof HTMLElement)) return false
  if (node.isContentEditable) return true
  const tag = node.tagName
  if (tag === "TEXTAREA") return true
  if (tag !== "INPUT") return false
  // A checkbox or a button-like input is not text entry; a text field is.
  const type = (node as HTMLInputElement).type
  return !["checkbox", "radio", "button", "submit", "reset", "file", "range"].includes(type)
}

/**
 * Attach the listener and return a teardown function.
 *
 * Exactly one instance of this should be live at a time — `<ScanListener>`
 * mounts it at the app root. Two listeners means every scan fires twice, and
 * the second one is a duplicate check-in (design-system.md §9.1).
 */
export function attachScanner(options: ScannerOptions): () => void {
  const {
    onBurst,
    captureInsideFields = false,
    thresholdMs = SCAN_KEY_THRESHOLD_MS,
    target = typeof window === "undefined" ? undefined : window,
  } = options

  if (!target) return () => {}

  let buffer = ""
  let timings: number[] = []
  let lastKey = 0
  let fast = true
  /** The most recent completed burst, for the Ctrl+Shift+D diagnostic. */
  let lastBurst: ScanBurst | null = null

  function reset() {
    buffer = ""
    timings = []
    fast = true
  }

  function handleKeydown(event: KeyboardEvent) {
    // The hidden diagnostic from design-system.md §9.4. Checked before the
    // text-entry guard so it works from any screen, including a focused field.
    if (event.ctrlKey && event.shiftKey && (event.key === "D" || event.key === "d")) {
      event.preventDefault()
      reportTimings(lastBurst, thresholdMs)
      return
    }

    // A modifier chord is never part of a barcode, and several of them edit the
    // field without emitting a keystroke this buffer can observe:
    // Alt/Cmd+Backspace delete a word or a whole line, Cmd+A followed by a
    // character replaces everything, Cmd+X cuts, Cmd+V pastes. Ignoring them
    // outright is what let the original bug survive its own fix — the field went
    // empty while the buffer still held "123456" and `fast` stayed true, so
    // Enter signed the person in as the number they had just deleted.
    //
    // Discarding is the only honest reading, because a chord can remove an
    // unknown amount of text. The pending burst is gone, so nothing downstream
    // acts on characters that are no longer on screen, and on the sign-in screen
    // the form's native submit picks the field up instead.
    if (event.ctrlKey || event.metaKey || event.altKey) {
      reset()
      return
    }

    if (!captureInsideFields && isTextEntry(event.target)) {
      reset()
      return
    }

    const now = performance.now()

    if (event.key === "Enter") {
      if (buffer.length === 0) return
      const burst: ScanBurst = {
        code: buffer,
        // A burst too short to have a measurable rhythm is treated as typed.
        fast: fast && buffer.length >= MIN_SCAN_LENGTH,
        timings,
        target: event.target,
      }
      lastBurst = burst
      reset()
      lastKey = now
      onBurst(burst)
      return
    }

    // Editing and caret keys. A barcode scanner never sends one, so any of these
    // means a person is correcting what they typed — and the buffer has to
    // follow, or it keeps characters that are no longer on screen. That was a
    // real bug: type a number, delete it, press Enter, and the deleted number
    // signed you in, because the buffer only ever grew (CLAUDE.md §13,
    // 2026-09-15).
    //
    // Everything except Backspace discards the burst, because none of them says
    // how much text moved or vanished. Backspace is exactly one character, so
    // the buffer can follow it precisely instead.
    if (DISCARDING_KEYS.has(event.key)) {
      reset()
      lastKey = now
      return
    }

    if (event.key === "Backspace") {
      buffer = buffer.slice(0, -1)
      timings.pop()
      // Emptying the buffer starts over rather than leaving `fast` false.
      // Nothing is pending, so there is nothing left to doubt — and a sticky
      // `fast = false` meant an idle Backspace on an empty field made the *next*
      // card scan report as typed, which demands a password from a
      // roster-imported user who does not have one yet (CLAUDE.md §7).
      if (buffer.length === 0) reset()
      else fast = false
      lastKey = now
      return
    }

    // Printable single characters only: a barcode is digits and ASCII, and
    // ignoring the rest keeps Tab, arrow keys and F-keys out of the buffer.
    if (event.key.length !== 1) return

    if (buffer.length > 0) {
      const gap = now - lastKey
      timings.push(gap)
      if (gap > thresholdMs) fast = false
    }
    lastKey = now
    buffer += event.key
  }

  target.addEventListener("keydown", handleKeydown as EventListener)
  return () => target.removeEventListener("keydown", handleKeydown as EventListener)
}

/**
 * Print the last burst's inter-key timings. The first question when a scan does
 * nothing is always "did it even see it?", and the second is "was it fast
 * enough?"; this answers both without a rebuild.
 */
function reportTimings(burst: ScanBurst | null, thresholdMs: number) {
  if (!burst) {
    // eslint-disable-next-line no-console
    console.info("[stockroom/scanner] no burst recorded yet")
    return
  }
  const gaps = burst.timings
  const max = gaps.length ? Math.max(...gaps) : 0
  const mean = gaps.length ? gaps.reduce((a, b) => a + b, 0) / gaps.length : 0
  // eslint-disable-next-line no-console
  console.info(
    `[stockroom/scanner] last burst ${JSON.stringify(burst.code)} — ` +
      `${gaps.length + 1} keys, gaps [${gaps.map((g) => g.toFixed(1)).join(", ")}] ms, ` +
      `max ${max.toFixed(1)}ms, mean ${mean.toFixed(1)}ms, ` +
      `threshold ${thresholdMs}ms → ${burst.fast ? "SCAN" : "TYPED"}`
  )
}

/**
 * Whether a burst could be a student number. Lives in `student-number.ts` now,
 * because the answer depends on the install's format setting; re-exported so
 * existing imports keep working.
 */
export { looksLikeStudentNumber } from "./student-number"

/** Trim a scanned serial the way the server does before looking it up. */
export function normalizeSerial(code: string): string {
  return code.trim()
}
