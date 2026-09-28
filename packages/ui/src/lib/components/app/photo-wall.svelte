<script lang="ts">
  /**
   * The sign-in photo wall: two strips of film, one rising and one falling,
   * behind the sign-in card (docs/design/signin-photo-wall.html §6).
   *
   * It is decoration, and every rule below follows from that one word. It never
   * blocks first paint, never takes a click, never speaks to a screen reader,
   * and draws nothing rather than something that looks broken. §9's invariant
   * is that no failure in this subsystem may delay, block or visibly break
   * sign-in.
   *
   * The two hard ones, because both are easy to undo later without noticing:
   *
   *   - `pointer-events: none` and `aria-hidden`. The sign-in input refocuses
   *     on blur, because a keyboard-wedge scanner types into whatever has focus
   *     (CLAUDE.md §10). A clickable photograph would start a blur/refocus fight
   *     with the one input in the app that must never lose focus.
   *   - No `await` between mount and the input being focusable. The fetch is
   *     fired from an effect and the strips appear when, or if, it resolves.
   *
   * Each strip is a conveyor rather than a looping CSS marquee. It holds only
   * the frames that fit on screen plus one, and when a frame scrolls off one
   * end the next photograph from the set joins at the other. So the set can
   * grow while the strip runs, with no jump, and a browser only ever loads the
   * few photographs on screen, whatever the set's size.
   */
  import { untrack } from "svelte"
  import { fileUrl, signInPhotos } from "../../api/index"

  /** Seconds a frame takes to scroll its own height, per strip. The two differ
   * so the strips never fall into step and read as one moving object. */
  const SECONDS_PER_FRAME = { up: 11, down: 13.4 }
  /** How often the set is read again: often while it fills, rarely after. */
  const POLL_FILLING_MS = 10_000
  const POLL_FULL_MS = 120_000

  type Frame = { id: number; src: string }
  type Strip = { frames: Frame[]; offset: number; cursor: number; el: HTMLDivElement | null }

  let set = $state<string[]>([])
  let height = $state(0)
  const reduced =
    typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches

  let nextId = 0
  const up = $state<Strip>({ frames: [], offset: 0, cursor: 0, el: null })
  const down = $state<Strip>({ frames: [], offset: 0, cursor: 1, el: null })

  /**
   * Read the set now and again. A failure is swallowed on purpose: the wall
   * must not generate log noise on a machine whose whole design goal is
   * working with no internet, and the next poll is the retry.
   */
  $effect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = () => {
      signInPhotos()
        .then((result) => {
          if (cancelled) return
          set = result.photos
          timer = setTimeout(poll, result.photos.length < result.size ? POLL_FILLING_MS : POLL_FULL_MS)
        })
        .catch(() => {
          if (!cancelled) timer = setTimeout(poll, POLL_FULL_MS)
        })
    }
    poll()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  })

  /** The next photograph for a strip. The two strips take alternate places in
   * the set, so they rarely show the same photograph at once. */
  function take(s: Strip): Frame {
    const src = set[s.cursor % set.length]
    s.cursor += 2
    return { id: nextId++, src }
  }

  /** The height of one frame, measured, since it follows the strip's width. */
  function frameHeight(s: Strip): number {
    const first = s.el?.firstElementChild
    return first instanceof HTMLElement ? first.offsetHeight : 0
  }

  /** Keep a strip holding enough frames to cover the screen plus one. */
  function top(s: Strip) {
    if (set.length === 0) return
    const h = frameHeight(s)
    const want = h > 0 ? Math.ceil(height / h) + 2 : 4
    while (s.frames.length < want) s.frames.push(take(s))
  }

  $effect(() => {
    if (set.length === 0 || height === 0) return
    untrack(() => {
      top(up)
      top(down)
    })
    // Once more after the first frames have laid out: until then there is no
    // frame to measure, and a guess can leave a strip short of the screen.
    const raf = requestAnimationFrame(() => {
      top(up)
      top(down)
    })
    return () => cancelAnimationFrame(raf)
  })

  /** Move a strip on by dt seconds, recycling any frame that has left. */
  function advance(s: Strip, dt: number, secondsPerFrame: number) {
    const h = frameHeight(s)
    if (h === 0 || s.frames.length === 0) return
    top(s)
    s.offset += (h / secondsPerFrame) * dt
    while (s.offset >= h) {
      s.offset -= h
      s.frames.shift()
      s.frames.push(take(s))
    }
  }

  $effect(() => {
    if (reduced || set.length === 0) return
    let raf = 0
    let last = performance.now()
    const tick = (now: number) => {
      // Clamped, so a tab coming back from the background moves on a frame's
      // worth rather than leaping by however long it was hidden.
      const dt = Math.min((now - last) / 1000, 0.1)
      last = now
      advance(up, dt, SECONDS_PER_FRAME.up)
      advance(down, dt, SECONDS_PER_FRAME.down)
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  })

  /**
   * Hide a frame whose photograph is gone, rather than show a broken-image
   * icon (§9). `visibility`, so the frame keeps its place in the strip.
   */
  function hideBroken(event: Event) {
    const img = event.currentTarget
    if (img instanceof HTMLImageElement) img.style.visibility = "hidden"
  }
</script>

{#if set.length > 0}
  <!--
    `aria-hidden` with `alt=""` inside: these photographs carry no meaning, and
    telling a screen reader to skip them is the correct answer (design-system.md
    §10 requires real alt text only for photos that carry information).

    `hidden xl:block` is the §7.2 breakpoint: below 1280px the strips are the
    first thing shed, because the card and its input are the screen's whole job.

    Absolutely positioned against <main>, so the card does not move by a pixel
    whether the wall is there or not.
  -->
  <div
    aria-hidden="true"
    class="pointer-events-none absolute inset-0 hidden select-none xl:block"
    bind:clientHeight={height}
  >
    <div class="wall absolute inset-y-0 left-[6%] w-[21%] overflow-hidden">
      <div class="strip up" bind:this={up.el} style:transform="translate3d(0, {-up.offset}px, 0)">
        {#each up.frames as frame (frame.id)}
          <div class="frame">
            <img src={fileUrl(frame.src)} alt="" decoding="async" onerror={hideBroken} />
          </div>
        {/each}
      </div>
    </div>

    <div class="wall absolute inset-y-0 right-[6%] w-[21%] overflow-hidden">
      <div class="strip down" bind:this={down.el} style:transform="translate3d(0, {down.offset}px, 0)">
        {#each down.frames as frame (frame.id)}
          <div class="frame">
            <img src={fileUrl(frame.src)} alt="" decoding="async" onerror={hideBroken} />
          </div>
        {/each}
      </div>
    </div>
  </div>
{/if}

<style>
  /*
    A scoped <style> block rather than utilities, for what Tailwind has no good
    spelling of: the mask and the sprocket holes. They are component-local, so
    they don't belong in tokens.css, which owns colours, radii, shadows and
    durations. The one colour here, the film base, is a token (--film).
  */

  /*
    The mask is not decoration. Without it the strips hard-clip a frame in half
    at the top and bottom edges, which reads as a rendering bug rather than as
    film running past the screen.
  */
  .wall {
    opacity: 0.6;
    -webkit-mask-image: linear-gradient(to bottom, transparent 0, #000 12%, #000 88%, transparent 100%);
    mask-image: linear-gradient(to bottom, transparent 0, #000 12%, #000 88%, transparent 100%);
  }

  /*
    The rising strip hangs from the top and moves up. The falling one is laid
    out bottom first from the bottom edge and moves down, so new frames enter
    at the top. Only `transform` changes, per design-system.md §11.
  */
  .strip {
    position: absolute;
    left: 0;
    right: 0;
    display: flex;
    will-change: transform;
  }

  .strip.up {
    top: 0;
    flex-direction: column;
  }

  .strip.down {
    bottom: 0;
    flex-direction: column-reverse;
  }

  /*
    One frame of film: the photograph with the film base around it, and a row
    of sprocket holes down each edge. The holes are the ground showing through,
    drawn as a repeating gradient so they scroll with the frame. The frames
    butt together with no gap, so the film reads as one continuous strip and a
    frame's height is exactly one step of the conveyor.
  */
  .frame {
    position: relative;
    flex: none;
    padding: 6px 22px;
    background: var(--film);
  }

  .frame::before,
  .frame::after {
    content: "";
    position: absolute;
    top: 0;
    bottom: 0;
    width: 8px;
    background: repeating-linear-gradient(
      to bottom,
      transparent 0 5px,
      var(--ground) 5px 13px,
      transparent 13px 18px
    );
    border-radius: 2px;
  }

  .frame::before {
    left: 7px;
  }

  .frame::after {
    right: 7px;
  }

  .frame img {
    display: block;
    width: 100%;
    aspect-ratio: 3 / 2;
    object-fit: cover;
    border-radius: 2px;
    background: var(--raised);
    filter: saturate(0.75);
  }

  /*
    Non-negotiable (design-system.md §10). With reduced motion the script
    never starts the conveyor, so the strips stand still as a collage.
  */
</style>
