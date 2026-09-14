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

    // A modifier chord is never part of a barcode.
    if (event.ctrlKey || event.metaKey || event.altKey) return

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
 * Whether a burst could be a student number: digits only, 1 to 32 of them.
 *
 * Mirrors `NormalizeStudentNumber` server-side (CLAUDE.md §13, 2026-09-08).
 * Cards encode six digits, but the bound is a mis-scan guard rather than a
 * format, so a reissued or imported number still works. The sign-in screen uses
 * this to tell an item barcode scanned with nobody signed in from a real login
 * attempt, and says "sign in first" instead of a generic bad-login error
 * (design-system.md §15 Q4).
 */
export function looksLikeStudentNumber(code: string): boolean {
  const trimmed = code.trim()
  return /^[0-9]{1,32}$/.test(trimmed)
}

/** Trim a scanned serial the way the server does before looking it up. */
export function normalizeSerial(code: string): string {
  return code.trim()
}
