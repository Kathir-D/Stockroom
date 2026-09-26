<script lang="ts">
  /**
   * The asset detail dialog (design-system.md §8.3): 560px, photo left, facts
   * right, history under that, actions in the footer.
   *
   * **Current custodian is visible to any signed-in viewer; historical
   * custodians are not.** Resolved 2026-09-12: letting a student find who has the
   * lens they want outweighs withholding it, and this project has no separate
   * school privacy officer to clear the open version with. It applies only to the
   * *current* holder — `GetAssetHistory` is admin-only, which is why this
   * component only asks for the trail when the viewer is an admin. The rule is
   * enforced in the Go API, not here; this just doesn't make a request it knows
   * will be refused.
   */
  import PlusIcon from "@lucide/svelte/icons/plus"
  import XIcon from "@lucide/svelte/icons/x"
  import UndoIcon from "@lucide/svelte/icons/undo-2"
  import PencilIcon from "@lucide/svelte/icons/pencil"
  import CircleSlashIcon from "@lucide/svelte/icons/circle-slash"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import { Separator } from "@stockroom/ui/components/ui/separator"
  import * as api from "../../api/index"
  import type { AssetCustody, AssetDetail, CustodyRecord } from "../../api/types"
  import { dateTime, resolveStatus, shortDate } from "../../status"
  import PhotoFrame from "./photo-frame.svelte"
  import Serial from "./serial.svelte"
  import StatusDot from "./status-dot.svelte"
  import { router } from "../../stores/router.svelte"

  let {
    asset,
    open = $bindable(false),
    viewerId = null,
    isAdmin = false,
    /** False when the viewer is overdue; the add path closes everywhere at once. */
    canAdd = true,
    inCart = false,
    onAdd,
    onRemove,
    onCheckIn,
    onEdit,
    onMarkUnavailable,
    onViewHistory,
  }: {
    asset: AssetDetail | null
    open?: boolean
    viewerId?: string | null
    isAdmin?: boolean
    canAdd?: boolean
    inCart?: boolean
    onAdd?: (asset: AssetDetail) => void
    /** Same button as Add, pressed again (§8.3). */
    onRemove?: (asset: AssetDetail) => void
    onCheckIn?: (asset: AssetDetail, note: string) => Promise<void> | void
    onEdit?: (asset: AssetDetail) => void
    onMarkUnavailable?: (asset: AssetDetail) => void
    /** Admin pressed the holder's name: show that person's whole trail. */
    onViewHistory?: (custody: AssetCustody) => void
  } = $props()

  let history = $state<CustodyRecord[]>([])
  let historyError = $state<string | null>(null)
  let note = $state("")
  let busy = $state(false)

  const status = $derived(asset ? resolveStatus(asset, viewerId) : null)
  const isOut = $derived(asset?.custody != null)
  const path = $derived(asset?.category_path?.map((c) => c.name).join(" › ") ?? "")

  // Load the trail when an admin opens a new asset. A non-admin never asks:
  // GetAssetHistory refuses them, and a 403 in the console is noise, not news.
  $effect(() => {
    const id = asset?.id
    if (!open || !id || !isAdmin) {
      history = []
      historyError = null
      return
    }
    let cancelled = false
    api
      .assetHistory(id)
      .then((rows) => {
        if (!cancelled) history = rows
      })
      .catch((error: unknown) => {
        if (!cancelled) historyError = error instanceof Error ? error.message : String(error)
      })
    return () => {
      cancelled = true
    }
  })

  // A fresh dialog starts with an empty damage note.
  $effect(() => {
    if (open) note = ""
  })

  /** When the item last came back, the start of the window it could have gone missing in. */
  const lastReturn = $derived(
    history
      .map((r) => r.checked_in_at)
      .filter((t): t is string => !!t)
      .sort()
      .at(-1) ?? null,
  )

  function openActivity(window: boolean) {
    if (!asset) return
    const from = lastReturn ?? new Date(Date.now() - 7 * 24 * 3600 * 1000).toISOString()
    const query: Record<string, string> = window ? { from } : { item: asset.id }
    open = false
    router.go({ name: "admin", tab: "activity", query })
  }

  async function handleCheckIn() {
    if (!asset || !onCheckIn) return
    busy = true
    try {
      await onCheckIn(asset, note)
    } finally {
      busy = false
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Content>
    {#if asset && status}
      <Dialog.Header>
        <Dialog.Title class="pr-8">{asset.name}</Dialog.Title>
        {#if path}
          <Dialog.Description>{path}</Dialog.Description>
        {/if}
      </Dialog.Header>

      <div class="flex gap-4">
        <PhotoFrame src={asset.photo_url} alt={asset.name} class="size-32 rounded-sm" iconClass="size-7" />

        <dl class="grid min-w-0 flex-1 grid-cols-[auto_1fr] items-center gap-x-3 gap-y-2">
          <dt class="text-xs text-fg-muted">Status</dt>
          <dd><StatusDot {status} variant="chip" /></dd>

          <dt class="text-xs text-fg-muted">Serial</dt>
          <dd><Serial value={asset.serial_number} /></dd>

          <!--
            Internal key, admin-only. The database generates it and nobody types
            it; the serial above is what a student tracks and what the scanner
            reads (CLAUDE.md §6.2). Not shortened to a unit number the way a
            serial is, because it sits right beside one and two identifiers
            rendering as the same bare digit would say nothing.
          -->
          {#if isAdmin}
            <dt class="text-xs text-fg-muted">Asset tag</dt>
            <dd><Serial value={asset.asset_tag} label="asset tag" /></dd>
          {/if}

          {#if asset.condition}
            <dt class="text-xs text-fg-muted">Condition</dt>
            <dd class="truncate text-fg">{asset.condition}</dd>
          {/if}

          {#if asset.custody}
            {@const held = asset.custody}
            {@const who = held.custodian_id === viewerId ? "You" : held.custodian_name}
            <dt class="text-xs text-fg-muted">Held by</dt>
            <dd class="truncate text-fg">
              <!--
                For an admin the name is a control: it opens that person's custody
                trail (2026-09-16). This dialog already shows what happened to the
                *item*; the other half of the question — what else does this person
                have, and do they bring it back — was only reachable by leaving
                and going to the admin lists.

                The section below is the asset's trail and stays admin-only for
                the same reason it always was: GetAssetHistory refuses a student.
              -->
              {#if isAdmin && onViewHistory}
                <button
                  type="button"
                  class="rounded-sm underline decoration-dotted underline-offset-4 hover:text-fg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-(--ring)"
                  onclick={() => onViewHistory(held)}
                >
                  {who}
                </button>
              {:else}
                {who}
              {/if}
              {#if isAdmin && held.student_number}
                <span class="ml-1 text-fg-muted">
                  <Serial value={held.student_number} label="student number" />
                </span>
              {/if}
            </dd>

            <dt class="text-xs text-fg-muted">Checked out</dt>
            <dd class="text-fg">{shortDate(asset.custody.checked_out_at)}</dd>

            <dt class="text-xs text-fg-muted">Due</dt>
            <dd class="text-fg">{shortDate(asset.custody.due_at) || "no due date"}</dd>
          {/if}
        </dl>
      </div>

      {#if asset.description}
        <p class="text-fg-muted">{asset.description}</p>
      {/if}

      {#if isOut && onCheckIn}
        <div class="flex flex-col gap-1.5">
          <Label for="checkin-note">Damage note (optional)</Label>
          <Input
            id="checkin-note"
            bind:value={note}
            placeholder="Lens cap missing, rear element scuffed, …"
          />
        </div>
      {/if}

      {#if isAdmin}
        <Separator />
        <section class="flex flex-col gap-1.5">
          <h3 class="text-xs font-semibold tracking-wide text-fg-muted uppercase">
            Custody history
          </h3>
          {#if historyError}
            <p class="text-status-overdue">{historyError}</p>
          {:else if history.length === 0}
            <p class="text-fg-faint">Never checked out.</p>
          {:else}
            <ul class="max-h-40 overflow-y-auto">
              {#each history as row (row.id)}
                <li class="flex items-baseline justify-between gap-3 border-b border-line py-1 last:border-b-0">
                  <span class="truncate text-fg">{row.custodian_name}</span>
                  <span class="shrink-0 text-xs text-fg-faint">
                    {dateTime(row.checked_out_at)} →
                    {row.checked_in_at ? dateTime(row.checked_in_at) : "still out"}
                    {#if row.days_overdue > 0}
                      <span class="text-status-overdue">· {row.days_overdue}d late</span>
                    {/if}
                  </span>
                </li>
              {/each}
            </ul>
          {/if}
          <!-- The missing-item question (ROADMAP §2.5): what happened at the
               closet between this item's last return and now. The camera's
               visits, sign-ins and scans in that window are on the activity
               timeline; the admin draws the conclusion. -->
          <div class="flex flex-wrap gap-2 pt-1">
            <Button variant="secondary" size="sm" onclick={() => openActivity(true)}>
              Closet activity since {lastReturn ? "its last return" : "last week"}
            </Button>
            <Button variant="ghost" size="sm" onclick={() => openActivity(false)}>This item's log</Button>
          </div>
        </section>
      {/if}

      <Dialog.Footer class="gap-2">
        {#if isAdmin}
          {#if onEdit}
            <Button variant="ghost" onclick={() => onEdit?.(asset)}>
              <PencilIcon aria-hidden="true" />
              Edit
            </Button>
          {/if}
          {#if onMarkUnavailable && asset.status !== "unavailable" && !isOut}
            <Button variant="ghost" onclick={() => onMarkUnavailable?.(asset)}>
              <CircleSlashIcon aria-hidden="true" />
              Mark unavailable
            </Button>
          {/if}
        {/if}

        <!-- One primary button per view (§13). Check in when it's out, add when
             it isn't; never both. -->
        {#if isOut && onCheckIn}
          <Button onclick={handleCheckIn} disabled={busy}>
            <UndoIcon aria-hidden="true" />
            {busy ? "Checking in…" : "Check in"}
          </Button>
        {:else if inCart && onRemove}
          <!-- Already in the cart: the same button takes it back out, so undo
               lives where the action was rather than only on the cart page. -->
          <Button variant="secondary" onclick={() => onRemove?.(asset)}>
            <XIcon aria-hidden="true" />
            Remove from cart
          </Button>
        {:else if onAdd && asset.status === "available"}
          <Button onclick={() => onAdd?.(asset)} disabled={!canAdd || inCart}>
            <PlusIcon aria-hidden="true" />
            {inCart ? "In cart" : canAdd ? "Add to cart" : "Return your overdue item first"}
          </Button>
        {/if}
      </Dialog.Footer>
    {/if}
  </Dialog.Content>
</Dialog.Root>
