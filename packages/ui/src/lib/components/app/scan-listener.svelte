<script lang="ts">
  /**
   * Headless. Wraps `lib/scanner.ts` and reports each completed keystroke burst
   * (design-system.md §5.2, §9).
   *
   * **Exactly one instance mounts, at the app root.** Two listeners means every
   * scan fires twice, and the second one is a duplicate check-in (§9.1). Screens
   * don't mount their own; they change what the root handler does with a burst,
   * because which endpoint a scan hits is decided by the active screen and never
   * by the payload (§9.2).
   */
  import { attachScanner, type ScanBurst } from "../../scanner"

  let {
    onBurst,
    /**
     * True only on the sign-in screen, whose input *is* the scan target. Anywhere
     * else the burst has to be ignored while focus sits in a text field, or typing
     * a search query fires phantom scans (§9.3).
     */
    captureInsideFields = false,
  }: {
    onBurst: (burst: ScanBurst) => void
    captureInsideFields?: boolean
  } = $props()

  // An effect rather than onMount, because `captureInsideFields` flips when the
  // app moves between the sign-in screen and the shell. The teardown is still
  // exact, so there is never more than one listener attached at a time.
  $effect(() => {
    const capture = captureInsideFields
    return attachScanner({ onBurst: (burst) => onBurst(burst), captureInsideFields: capture })
  })
</script>
