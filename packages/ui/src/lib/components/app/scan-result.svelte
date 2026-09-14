<script lang="ts">
  /**
   * The surface shown after a scan, and after a checkout (design-system.md §8.6,
   * §8.5).
   *
   * `data-density="kiosk"`, drawn *over* the current screen so context isn't
   * lost, dismissing on confirm, cancel, Escape, or a fresh scan.
   *
   * The branch that matters most: **scanning a checked-out item has already
   * checked it in by the time this renders.** No confirm press — `CLAUDE.md` §1.5
   * wins over the earlier confirm-first idea (§15 Q2). Scanning an *available*
   * item still takes a press to add, because that path opens the same
   * detail/add-to-cart flow as clicking the item rather than an irreversible
   * action.
   *
   * Announced `aria-live="assertive"`: a scan result is the one thing in this app
   * that must interrupt a screen reader (§10).
   */
  import CheckIcon from "@lucide/svelte/icons/check"
  import PlusIcon from "@lucide/svelte/icons/plus"
  import LogOutIcon from "@lucide/svelte/icons/log-out"
  import ScanBarcodeIcon from "@lucide/svelte/icons/scan-barcode"
  import ArrowDownIcon from "@lucide/svelte/icons/arrow-down-to-line"
  import ArrowUpIcon from "@lucide/svelte/icons/arrow-up-from-line"
  import SearchIcon from "@lucide/svelte/icons/search"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Collapsible from "@stockroom/ui/components/ui/collapsible"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import { cn } from "@stockroom/ui/utils"
  import type { AssetDetail } from "../../api/types"
  import { resolveStatus, shortDate } from "../../status"
  import { scanStore } from "../../stores/scan.svelte"
  import PhotoFrame from "./photo-frame.svelte"
  import Serial from "./serial.svelte"
  import StatusDot from "./status-dot.svelte"

  let {
    canAdd = true,
    inCart = false,
    onAdd,
    onSaveNote,
    onSignOut,
  }: {
    canAdd?: boolean
    inCart?: boolean
    onAdd?: (asset: AssetDetail) => void
    /** Persists the damage note against the custody event the scan just closed. */
    onSaveNote?: (custodyEventId: string, note: string) => Promise<void>
    onSignOut?: () => void
  } = $props()

  const surface = $derived(scanStore.surface)

  let note = $state("")
  let noteOpen = $state(false)
  let noteSaved = $state(false)
  let noteBusy = $state(false)

  // A fresh surface resets the note field, because a new scan replaces the
  // contents outright rather than layering on the previous one.
  $effect(() => {
    void surface
    note = ""
    noteOpen = false
    noteSaved = false
  })

  function close() {
    scanStore.close()
  }

  function onKeydown(event: KeyboardEvent) {
    // Escape closes the topmost layer only, which here is this surface (§10).
    if (event.key === "Escape") {
      event.stopPropagation()
      close()
    }
  }

  async function saveNote(custodyEventId: string) {
    if (!onSaveNote || !note.trim()) return
    noteBusy = true
    try {
      await onSaveNote(custodyEventId, note)
      noteSaved = true
    } finally {
      noteBusy = false
    }
  }

  const status = $derived(
    surface?.kind === "scan" ? resolveStatus(surface.result.asset) : null
  )
</script>

<svelte:window onkeydown={surface ? onKeydown : undefined} />

{#if surface}
  <!--
    A plain fixed overlay rather than a `dialog`: the surface has to be replaceable
    mid-render by the next scan, and Bits UI's focus trap fights that by restoring
    focus to a trigger that no longer describes what is on screen.
  -->
  <div
    data-density="kiosk"
    class="fixed inset-0 z-50 flex flex-col items-center justify-center gap-(--gutter) bg-ground/95 p-(--gutter)"
    role="alertdialog"
    aria-modal="true"
    aria-label="Scan result"
  >
    <div
      aria-live="assertive"
      class={cn(
        "w-full max-w-[720px] rounded-xl bg-surface p-(--gutter) shadow-elev-3",
        "transition-colors duration-(--dur-slow) ease-(--ease-brand)",
        scanStore.confirming && "bg-status-available-bg"
      )}
    >
      {#if surface.kind === "scan" && status}
        {@const asset = surface.result.asset}
        <div class="flex items-start gap-(--gutter)">
          <PhotoFrame src={asset.photo_url} alt={asset.name} class="size-40 rounded-sm" iconClass="size-9" />
          <div class="flex min-w-0 flex-1 flex-col gap-2">
            {#if surface.result.action === "checked_in"}
              <p class="flex items-center gap-2 text-status-available">
                <CheckIcon class="size-5" aria-hidden="true" />
                <span class="font-semibold">Checked in</span>
              </p>
              <h2 class="truncate text-2xl font-semibold text-fg">{asset.name}</h2>
              <Serial value={asset.serial_number} class="text-lg" />
              {#if surface.result.returned_from}
                <p class="text-fg-muted">
                  Returned from {surface.result.returned_from.custodian_name}
                  {#if surface.result.returned_from.overdue}
                    <span class="text-status-overdue">· was overdue</span>
                  {/if}
                </p>
              {/if}
              <p class="text-fg-faint">Put it back on the shelf.</p>
            {:else}
              <h2 class="truncate text-2xl font-semibold text-fg">{asset.name}</h2>
              <Serial value={asset.serial_number} class="text-lg" />
              <StatusDot {status} variant="chip" class="self-start" />
              {#if asset.category_path?.length}
                <p class="truncate text-fg-muted">
                  {asset.category_path.map((c) => c.name).join(" › ")}
                </p>
              {/if}
              {#if asset.status === "unavailable"}
                <p class="text-fg-muted">
                  {asset.condition ?? "Marked unavailable — ask an admin before taking it."}
                </p>
              {/if}
            {/if}
          </div>
        </div>

        <!-- The damage note. Collapsed by default and expanded inline, because the
             check-in already happened: this annotates it, it doesn't gate it. -->
        {#if surface.result.action === "checked_in" && onSaveNote && surface.result.returned_from}
          {@const eventId = surface.result.returned_from.custody_event_id}
          <Collapsible.Root bind:open={noteOpen} class="mt-(--gutter)">
            <Collapsible.Trigger class="text-fg-muted underline-offset-4 hover:text-fg hover:underline">
              Add a note
            </Collapsible.Trigger>
            <Collapsible.Content>
              <div class="mt-2 flex flex-col gap-2">
                <Label for="scan-note">What's wrong with it?</Label>
                <div class="flex gap-2">
                  <Input
                    id="scan-note"
                    bind:value={note}
                    placeholder="Lens cap missing, rear element scuffed, …"
                  />
                  <Button
                    size="tap"
                    variant="secondary"
                    disabled={noteBusy || !note.trim() || noteSaved}
                    onclick={() => saveNote(eventId)}
                  >
                    {noteSaved ? "Saved" : noteBusy ? "Saving…" : "Save"}
                  </Button>
                </div>
              </div>
            </Collapsible.Content>
          </Collapsible.Root>
        {/if}

        <div class="mt-(--gutter) flex justify-end gap-2">
          {#if surface.result.checkable && onAdd}
            <Button size="tap" variant="ghost" onclick={close}>Cancel</Button>
            <!-- Autofocused: the next thing anyone does here is add it. -->
            <Button
              size="tap"
              disabled={!canAdd || inCart}
              onclick={() => {
                onAdd?.(asset)
                close()
              }}
            >
              <PlusIcon aria-hidden="true" />
              {inCart ? "Already in cart" : canAdd ? "Add to cart" : "Return your overdue item first"}
            </Button>
          {:else}
            <Button size="tap" onclick={close}>Close</Button>
          {/if}
        </div>
      {:else if surface.kind === "unknown"}
        <h2 class="text-2xl font-semibold text-fg">Not a Stockroom item</h2>
        <p class="mt-2 font-mono text-lg text-fg-muted select-all">{surface.code}</p>
        <p class="mt-1 text-fg-faint">
          Nothing in the inventory has that serial. Check the sticker, or ask an admin to add it.
        </p>
        <div class="mt-(--gutter) flex justify-end">
          <Button size="tap" onclick={close}>Close</Button>
        </div>
      {:else if surface.kind === "error"}
        <h2 class="text-2xl font-semibold text-status-overdue">Scan failed</h2>
        <p class="mt-2 font-mono text-lg text-fg-muted select-all">{surface.code}</p>
        <p class="mt-1 text-fg">{surface.message}</p>
        <div class="mt-(--gutter) flex justify-end">
          <Button size="tap" onclick={close}>Close</Button>
        </div>
      {:else if surface.kind === "checkout"}
        <p class="flex items-center gap-2 text-status-available">
          <CheckIcon class="size-5" aria-hidden="true" />
          <span class="font-semibold">
            {surface.result.items.length}
            {surface.result.items.length === 1 ? "item" : "items"} checked out
          </span>
        </p>
        <h2 class="mt-1 text-2xl font-semibold text-fg">
          Due {shortDate(surface.result.due_at)}
        </h2>
        <p class="text-fg-muted">To {surface.result.custodian_name}</p>
        <ul class="mt-(--gutter) max-h-56 overflow-y-auto">
          {#each surface.result.items as item (item.custody_event_id)}
            <li class="flex items-baseline justify-between gap-3 border-b border-line py-1.5 last:border-b-0">
              <span class="truncate text-fg">{item.name}</span>
              <Serial value={item.serial_number ?? item.asset_tag} class="shrink-0" />
            </li>
          {/each}
        </ul>
        <!-- The sign-out prompt is required by CLAUDE.md §7: the closet PC is
             shared, so the next person must not inherit this session. -->
        <div class="mt-(--gutter) flex justify-end gap-2">
          <Button size="tap" variant="ghost" onclick={close}>Done</Button>
          <Button size="tap" onclick={() => onSignOut?.()}>
            <LogOutIcon aria-hidden="true" />
            Sign out
          </Button>
        </div>
      {/if}
    </div>

    <!-- The session scan log, persisting under the surface so someone returning a
         six-item kit can verify all six landed (§8.6). -->
    {#if scanStore.log.length > 0}
      <div class="w-full max-w-[720px] rounded-xl border border-line-strong bg-ground/80 p-3">
        <h3 class="flex items-center gap-2 text-xs font-semibold tracking-wide text-fg-muted uppercase">
          <ScanBarcodeIcon class="size-3.5" aria-hidden="true" />
          Recent scans
        </h3>
        <ul class="mt-1.5">
          {#each scanStore.log as entry (entry.id)}
            <li class="flex items-center gap-3 border-b border-line py-1 text-sm last:border-b-0">
              <span class="w-16 shrink-0 text-fg-faint tabular-nums">
                {entry.at.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}
              </span>
              {#if entry.direction === "in"}
                <ArrowDownIcon class="size-3.5 shrink-0 text-status-available" aria-label="checked in" />
              {:else if entry.direction === "out"}
                <ArrowUpIcon class="size-3.5 shrink-0 text-status-out" aria-label="checked out" />
              {:else}
                <SearchIcon class="size-3.5 shrink-0 text-fg-faint" aria-label="looked up" />
              {/if}
              <span class="min-w-0 flex-1 truncate text-fg">{entry.name}</span>
              <Serial value={entry.serial} class="shrink-0" />
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  </div>
{/if}
