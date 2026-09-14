/**
 * What the scan surface is currently showing, and the session scan log beneath
 * it (design-system.md §8.6).
 *
 * A store rather than component state because of one rule: **any new scan while
 * the surface is open replaces its contents immediately.** Someone with an
 * armful of gear scans continuously and should never have to press Cancel
 * between items. That only works if the surface reads from one place that the
 * root scan handler can overwrite mid-render.
 */

import type { CheckoutResult, ScanResult } from "../api/types"

/** What the kiosk surface is showing, if anything. */
export type ScanSurface =
  /** A scan the server understood: detail-and-add, or an already-done check-in. */
  | { kind: "scan"; result: ScanResult }
  /** A serial the server doesn't know. Shows the scanned string verbatim. */
  | { kind: "unknown"; code: string }
  /** A scan that failed for some other reason — a 409, a dead server. */
  | { kind: "error"; code: string; message: string }
  /** Checkout confirmation, which §8.5 routes through this same surface. */
  | { kind: "checkout"; result: CheckoutResult }

export interface ScanLogEntry {
  id: string
  at: Date
  name: string
  serial: string | null
  direction: "in" | "out" | "looked-up"
}

/** Five, because that is a six-item kit minus the one you're holding. */
const LOG_LIMIT = 5

class ScanStore {
  surface = $state<ScanSurface | null>(null)
  /**
   * The last few scans, so someone returning a six-item kit can verify all six
   * landed. Persists under the surface between scans; cleared on sign-out.
   */
  log = $state<ScanLogEntry[]>([])
  /**
   * Flips true for --dur-slow after a check-in, which is what drives the green
   * confirmation flash rather than a CSS animation on mount (§8.6, §11).
   */
  confirming = $state(false)

  private confirmTimer: ReturnType<typeof setTimeout> | null = null

  show(surface: ScanSurface) {
    this.surface = surface
    if (surface.kind === "scan") {
      this.record(surface.result)
      if (surface.result.action === "checked_in") this.flashConfirmation()
    }
  }

  close() {
    this.surface = null
    this.confirming = false
    if (this.confirmTimer) clearTimeout(this.confirmTimer)
    this.confirmTimer = null
  }

  clear() {
    this.close()
    this.log = []
  }

  private record(result: ScanResult) {
    const entry: ScanLogEntry = {
      id: `${result.asset.id}-${Date.now()}`,
      at: new Date(),
      name: result.asset.name,
      serial: result.asset.serial_number,
      direction: result.action === "checked_in" ? "in" : "looked-up",
    }
    this.log = [entry, ...this.log].slice(0, LOG_LIMIT)
  }

  /** Called by the cart page so a checkout shows up in the same log. */
  recordCheckout(result: CheckoutResult) {
    const entries: ScanLogEntry[] = result.items.map((item) => ({
      id: `${item.asset_id}-${Date.now()}`,
      at: new Date(),
      name: item.name,
      serial: item.serial_number,
      direction: "out" as const,
    }))
    this.log = [...entries, ...this.log].slice(0, LOG_LIMIT)
  }

  private flashConfirmation() {
    this.confirming = true
    if (this.confirmTimer) clearTimeout(this.confirmTimer)
    // --dur-slow. Read from the stylesheet would be cuter and more fragile; the
    // number is documented in tokens.css and in §11's table.
    this.confirmTimer = setTimeout(() => (this.confirming = false), 320)
  }
}

export const scanStore = new ScanStore()
