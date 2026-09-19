/**
 * The package entry point: what an app imports as `@stockroom/ui`.
 *
 * Deliberately small. A host app renders `<StockroomApp>` and nothing else
 * (design-system.md §14, step 10: "if it turns into a rewrite, something leaked
 * into the app"), so the rest of this file is the handful of things a host
 * legitimately needs — the API client, the scanner threshold for the Week 7
 * tuning session, and the stores, for a test.
 *
 * Individual components and screens are *not* re-exported here. They have
 * subpath exports (`@stockroom/ui/components/app/serial.svelte`) so a screen
 * importing one doesn't drag the whole design system into the module graph, and
 * so shadcn-svelte's generated imports resolve identically from both apps.
 */

export { default as StockroomApp } from "./app.svelte"

export * from "./api/index"
export * from "./keep-alive"
export * from "./scanner"
export * from "./status"
export * from "./kits"
export * from "./due"
export { cn } from "./utils"

export { cart } from "./stores/cart.svelte"
export { cartItems } from "./stores/cart-items.svelte"
export { catalog, groupByModel, type ModelGroup } from "./stores/catalog.svelte"
export { kits } from "./stores/kits.svelte"
export { router, type AdminTab, type Route } from "./stores/router.svelte"
export { scanStore, type ScanLogEntry, type ScanSurface } from "./stores/scan.svelte"
export { session } from "./stores/session.svelte"
