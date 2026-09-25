<script lang="ts">
  /**
   * The sign-in photo wall: two slowly scrolling columns of department
   * photography behind the sign-in card (docs/design/signin-photo-wall.html §6).
   *
   * It is decoration, and every rule below follows from that one word. It never
   * blocks first paint, never retries, never takes a click, never speaks to a
   * screen reader, and disappears entirely rather than degrading into something
   * that looks broken. §9's invariant is that no failure in this subsystem may
   * delay, block or visibly break sign-in; the wall is the first thing to go
   * and the last thing to complain.
   *
   * The two hard ones, because both are easy to undo later without noticing:
   *
   *   - `pointer-events: none` and `aria-hidden`. The sign-in input refocuses
   *     on blur, because a keyboard-wedge scanner types into whatever has focus
   *     (CLAUDE.md §10). A clickable photograph would start a blur/refocus fight
   *     with the one input in the app that must never lose focus -- the same
   *     shape of bug that hung the tab when the restore report was a modal.
   *   - No `await` between mount and the input being focusable. The fetch is
   *     fired from an effect and the columns appear when, or if, it resolves.
   *     This is the mechanism by which CLAUDE.md §2 survives: the sign-in
   *     screen's time to interactive does not depend on a network call, so a
   *     hung or absent server for this endpoint costs exactly nothing.
   */
  import { fileUrl, signInPhotos } from "../../api/index"

  /**
   * How many tiles a column needs before it stops repeating itself.
   *
   * The endpoint hands out whatever is ready, which on a drained reel is fewer
   * than a batch and sometimes zero (§2, burst protection). A column built from
   * two tiles would scroll a visible gap; repeating the short list until it
   * fills is the degradation the design asks for -- density suffers, nothing
   * looks broken.
   */
  const MIN_TILES_PER_COLUMN = 6

  /**
   * Seconds each tile takes to scroll past, per column: 88s and 107s over the
   * eight tiles a column held when those numbers were chosen (110s and 134s
   * until 2026-09-25, when the wall was sped up by a fifth on request).
   *
   * A loop's duration is this times the column's length, not a constant. The
   * strip loops by translating -50% of itself, so a fixed duration makes the
   * *speed* depend on how many photographs arrived -- doubling the batch to 32
   * would have doubled it, and the whole case for the wall rests on it moving
   * slowly enough that peripheral vision stops tracking it. Per tile, the speed
   * is the same for any batch, a long batch just takes longer to come round,
   * and the two rates keep the 88:107 ratio that stops the columns falling
   * into step.
   */
  const SECONDS_PER_TILE = { up: 88 / 8, down: 107 / 8 }

  let columns = $state<{ left: string[]; right: string[] } | null>(null)
  const left = $derived(columns ? strip(columns.left) : [])
  const right = $derived(columns ? strip(columns.right) : [])

  /**
   * Fetch once, on mount, and never again.
   *
   * No reactive reads in the effect body, so Svelte runs it exactly once. A
   * failure -- no server, no network, no rclone -- is swallowed on purpose: the
   * wall must not generate traffic or log noise on a machine whose whole design
   * goal is working with no internet, and there is nothing a retry could
   * achieve that the next sign-in would not.
   */
  $effect(() => {
    let cancelled = false
    signInPhotos()
      .then((result) => {
        if (cancelled || result.photos.length === 0) return
        columns = split(result.photos)
      })
      .catch(() => {
        // Deliberately silent. See above.
      })
    return () => {
      cancelled = true
    }
  })

  /**
   * Shuffle the batch and deal it between the two columns.
   *
   * The server hands out tiles in a stable order, so without this the same
   * batch would always read the same way down the page. Dealing alternately
   * rather than splitting in half keeps the two columns the same length when
   * the count is odd.
   */
  function split(photos: string[]): { left: string[]; right: string[] } {
    const shuffled = [...photos]
    for (let i = shuffled.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1))
      ;[shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]]
    }
    return {
      left: shuffled.filter((_, i) => i % 2 === 0),
      right: shuffled.filter((_, i) => i % 2 === 1),
    }
  }

  /**
   * Hide a tile whose file is already gone.
   *
   * A batch races the reaper: the URLs are valid for the TTL, but a tab left
   * open past it will 404 on a re-request, and §9 is explicit that this must
   * never show a broken-image icon.
   *
   * `visibility` rather than `display`, so the tile keeps its slot in the
   * column. Removing it from the flow would shorten the strip, and the marquee
   * loops by translating exactly -50% of that strip -- so a collapsed tile
   * would put the loop point out of register and produce a visible jump once a
   * cycle. The same URL appears in both halves of the doubled list and fails in
   * both, which keeps the halves identical either way; this just means the
   * geometry never depends on that.
   */
  function hideBrokenTile(event: Event) {
    const img = event.currentTarget
    if (img instanceof HTMLImageElement) img.style.visibility = "hidden"
  }

  /** One loop's duration for a rendered (doubled) strip: half of it is one cycle. */
  function cycle(rendered: string[], secondsPerTile: number): string {
    return `${(rendered.length / 2) * secondsPerTile}s`
  }

  /**
   * The list a column actually renders: repeated up to a workable length, then
   * **doubled**.
   *
   * The doubling is what makes the marquee seamless. The strip animates from
   * `translateY(0)` to `translateY(-50%)`, and at -50% the second copy sits
   * exactly where the first began, so the loop point is invisible. Any other
   * end value, or an odd number of copies, produces a jump once a cycle.
   */
  function strip(photos: string[]): string[] {
    if (photos.length === 0) return []
    const filled = [...photos]
    while (filled.length < MIN_TILES_PER_COLUMN) filled.push(...photos)
    return [...filled, ...filled]
  }
</script>

{#if columns}
  <!--
    `aria-hidden` with `alt=""` inside: these photographs carry no meaning, and
    telling a screen reader to skip them is the correct answer rather than an
    omission (design-system.md §10 requires real alt text only for photos that
    carry information).

    `hidden xl:block` is the §7.2 breakpoint: below 1280px the columns are the
    first thing shed, because the card and its input are the screen's whole job.

    Absolutely positioned against <main>, so the card does not move by a pixel
    whether the wall is there or not -- somebody scans at that box a hundred
    times a week and muscle memory is real.
  -->
  <div aria-hidden="true" class="pointer-events-none absolute inset-0 hidden select-none xl:block">
    <div class="wall absolute inset-y-0 left-0 w-[27%] overflow-hidden">
      <div class="strip up" style:animation-duration={cycle(left, SECONDS_PER_TILE.up)}>
        {#each left as src, i (i)}
          <img
            class="tile"
            src={fileUrl(src)}
            alt=""
            onerror={hideBrokenTile}
          />
        {/each}
      </div>
    </div>

    <div class="wall absolute inset-y-0 right-0 w-[27%] overflow-hidden">
      <div class="strip down" style:animation-duration={cycle(right, SECONDS_PER_TILE.down)}>
        {#each right as src, i (i)}
          <img
            class="tile"
            src={fileUrl(src)}
            alt=""
            onerror={hideBrokenTile}
          />
        {/each}
      </div>
    </div>
  </div>
{/if}

<style>
  /*
    A scoped <style> block rather than utilities, for the three things Tailwind
    has no good spelling of: two keyframes, a mask, and the reduced-motion
    override. They are component-local -- a marquee is not a design token -- so
    they do not belong in tokens.css, which owns colours, radii, shadows and
    durations and should stay that short list.
  */

  /*
    The mask is not decoration. Without it the columns hard-clip a photograph in
    half at the top and bottom edges, which reads as a rendering bug rather than
    as a wall continuing past the screen.
  */
  .wall {
    opacity: 0.5;
    filter: saturate(0.72);
    -webkit-mask-image: linear-gradient(to bottom, transparent 0, #000 12%, #000 88%, transparent 100%);
    mask-image: linear-gradient(to bottom, transparent 0, #000 12%, #000 88%, transparent 100%);
  }

  .strip {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    display: flex;
    flex-direction: column;
    gap: 14px;
    /*
      The bottom padding is the gap again, and it is load-bearing rather than
      spacing. `gap` sits *between* items, so a doubled list of 2N tiles is
      2N*tile + (2N-1)*14 tall while one cycle of the marquee is N*tile +
      N*14 -- the -50% the animation translates by lands 7px short, and the
      loop visibly jolts once every couple of minutes. The trailing padding
      restores the missing gap so half the strip is exactly one cycle.
    */
    padding: 0 14px 14px;
    will-change: transform;
  }

  /*
    88s and 107s for eight tiles: slow enough that peripheral vision stops
    tracking the motion, and coprime so the two columns never fall into step and
    start reading as one moving object. The inline animation-duration scales
    these with the column's length (SECONDS_PER_TILE); the values here are the
    fallback for eight. `transform` only -- design-system.md §11 bans animating
    height, width and box-shadow outright, and the closet PC's webview is not a
    fast machine.
  */
  .strip.up {
    animation: wall-up 88s linear infinite;
  }

  .strip.down {
    animation: wall-down 107s linear infinite;
  }

  @keyframes wall-up {
    from {
      transform: translate3d(0, 0, 0);
    }
    to {
      transform: translate3d(0, -50%, 0);
    }
  }

  @keyframes wall-down {
    from {
      transform: translate3d(0, -50%, 0);
    }
    to {
      transform: translate3d(0, 0, 0);
    }
  }

  .tile {
    display: block;
    width: 100%;
    flex: none;
    aspect-ratio: 3 / 2;
    object-fit: cover;
    border-radius: var(--radius-sm);
    background: var(--raised);
    box-shadow: 0 0 0 1px rgb(0 0 0 / 0.25);
  }

  /*
    Non-negotiable (design-system.md §10). tokens.css already collapses every
    animation globally under this query, which would leave the strip parked at
    translate(0) -- the top of the list, with the mask fading out most of what
    is there. Naming a fixed offset instead makes it a deliberate still collage.
  */
  @media (prefers-reduced-motion: reduce) {
    .strip {
      animation: none;
      transform: translate3d(0, -14%, 0);
    }
  }
</style>
