<script lang="ts">
  /**
   * The cart page and the checkout transition (design-system.md §8.4, §8.5).
   *
   * A route in the same shell rather than a drawer: the drawer ate 420px of a
   * 1024px Wails window for a panel you visit once per checkout, and a cart page
   * is the interaction every student already knows (§16, 2026-09-10).
   *
   * Two columns — line items left, commit panel right. The line items carry a
   * **live** status, so an item somebody else took while the cart sat idle turns
   * red here instead of failing inside the transaction.
   *
   * `POST /checkout` is called exactly once and is one transaction, so the UI
   * must never show partial success. The three outcomes are handled below:
   * success, `ErrConflict` naming what stopped being available, and
   * `ErrOverdueBlocked`, which shouldn't be reachable because the dock already
   * blocked it — but is handled anyway, because the server is the layer that
   * actually decides (§15 Q6).
   */
  import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left"
  import TrashIcon from "@lucide/svelte/icons/trash-2"
  import XIcon from "@lucide/svelte/icons/x"
  import LoaderIcon from "@lucide/svelte/icons/loader-circle"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import * as AlertDialog from "@stockroom/ui/components/ui/alert-dialog"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Checkbox } from "@stockroom/ui/components/ui/checkbox"
  import { Label } from "@stockroom/ui/components/ui/label"
  import * as Select from "@stockroom/ui/components/ui/select"
  import DueDatePicker from "@stockroom/ui/components/app/due-date-picker.svelte"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import PhotoFrame from "@stockroom/ui/components/app/photo-frame.svelte"
  import Serial from "@stockroom/ui/components/app/serial.svelte"
  import StatusDot from "@stockroom/ui/components/app/status-dot.svelte"
  import * as api from "../api/index"
  import type { CheckoutResult, Profile } from "../api/types"
  import { resolveStatus } from "../status"
  import { cart } from "../stores/cart.svelte"
  import { cartItems } from "../stores/cart-items.svelte"
  import { catalog } from "../stores/catalog.svelte"
  import { session } from "../stores/session.svelte"

  let {
    onBack,
    onCheckedOut,
  }: {
    onBack: () => void
    onCheckedOut: (result: CheckoutResult) => void
  } = $props()

  let submitting = $state(false)
  let clearOpen = $state(false)
  let conflictOpen = $state(false)
  let conflictMessage = $state("")
  let overrideOpen = $state(false)
  let error = $state<string | null>(null)

  /** Admin-only: check out on behalf of someone else (CLAUDE.md §7). */
  let users = $state<Profile[]>([])
  let custodianId = $state<string>("")
  let overrideOverdue = $state(false)

  const blocked = $derived(session.checkoutBlocked)
  const dueAt = $derived(cart.dueAt)
  const canSubmit = $derived(
    !submitting && cart.count > 0 && dueAt !== null && (!blocked || (session.isAdmin && overrideOverdue))
  )

  // Load the live statuses whenever the page opens or the cart changes size.
  $effect(() => {
    void cart.ids.length
    cartItems.refresh(catalog.units)
  })

  // Removing the last item returns to browse: an empty cart page is a dead end.
  $effect(() => {
    if (cart.isEmpty) onBack()
  })

  $effect(() => {
    if (!session.isAdmin) return
    let cancelled = false
    api
      .listUsers()
      .then((list) => {
        if (!cancelled) users = list
      })
      .catch(() => {
        // Not fatal: without the list, an admin checks out to themselves, which
        // is the default anyway.
      })
    return () => {
      cancelled = true
    }
  })

  const custodianLabel = $derived(() => {
    if (!custodianId) return `${session.displayName} (you)`
    const match = users.find((u) => u.id === custodianId)
    if (!match) return "Pick someone"
    return [match.first_name, match.last_name].filter(Boolean).join(" ") || (match.student_number ?? "")
  })

  async function submit() {
    if (!dueAt) return
    submitting = true
    error = null
    try {
      const result = await api.checkout({
        asset_ids: cart.ids,
        due_at: dueAt,
        // Blank means the actor, which is the only value a non-admin may send.
        ...(session.isAdmin && custodianId ? { custodian_id: custodianId } : {}),
        // A non-admin sending this at all is a 403, not a dropped field.
        ...(session.isAdmin && overrideOverdue ? { override_overdue: true } : {}),
      })
      cart.clear()
      cartItems.clear()
      await session.refresh()
      onCheckedOut(result)
    } catch (err) {
      if (err instanceof api.ApiError && err.isConflict) {
        conflictMessage = err.message
        // Re-read the lines so the page behind the dialog agrees with it: the
        // items that failed flip to their real status rather than still claiming
        // to be available.
        await cartItems.refresh()
        conflictOpen = true
      } else {
        error = err instanceof Error ? err.message : String(err)
      }
    } finally {
      submitting = false
    }
  }

  /** "Remove them and retry": drop whatever is no longer available. */
  function dropUnavailable() {
    const ids = cartItems.unavailableIds
    conflictOpen = false
    if (ids.length === 0) return
    cart.removeMany(ids)
    cartItems.refresh()
  }
</script>

<div class="flex flex-col gap-(--gutter)">
  <div class="flex items-center justify-between gap-3">
    <!-- Back to the category the user left, not the root of the tree: someone
         adding six items from one shelf shouldn't re-navigate after every trip. -->
    <Button variant="ghost" onclick={onBack}>
      <ArrowLeftIcon aria-hidden="true" />
      Back to browse
    </Button>
    <h1 class="text-lg font-semibold text-fg">
      Cart · {cart.count} {cart.count === 1 ? "item" : "items"}
    </h1>
  </div>

  {#if blocked}
    <div
      class="flex items-start gap-3 rounded-xl border border-status-overdue/40 bg-status-overdue-bg p-(--gutter)"
      role="alert"
    >
      <AlertTriangleIcon class="mt-0.5 size-4 shrink-0 text-status-overdue" aria-hidden="true" />
      <div class="min-w-0">
        <p class="font-medium text-status-overdue">
          {session.overdueItems.length === 1
            ? "You have an overdue item"
            : `You have ${session.overdueItems.length} overdue items`}
        </p>
        <ul class="mt-1 text-fg-muted">
          {#each session.overdueItems as row (row.id)}
            <li>
              {row.asset_name} · {row.serial_number ?? row.asset_tag} ·
              {row.days_overdue} {row.days_overdue === 1 ? "day" : "days"} late
            </li>
          {/each}
        </ul>
        {#if session.isAdmin}
          <p class="mt-2 text-fg-faint">
            As an admin you can override this for one checkout, in the panel on the right.
          </p>
        {/if}
      </div>
    </div>
  {/if}

  <div class="flex flex-col gap-(--gutter) lg:flex-row lg:items-start">
    <!-- Left: the line items. -->
    <div class="flex min-w-0 flex-1 flex-col gap-2">
      {#if cartItems.items.length === 0}
        <EmptyState title="Loading your cart…" />
      {:else}
        {#each cartItems.items as item (item.id)}
          {@const status = resolveStatus(item, session.profile?.id ?? null)}
          <!-- One rounded card per line item, matching the browse list. -->
          <div
            class="flex items-center gap-3 rounded-(--radius) border border-line px-(--gutter) py-2"
          >
            <PhotoFrame src={item.photo_url} alt={item.name} class="size-10" />
            <div class="flex min-w-0 flex-1 flex-col">
              <span class="truncate font-medium text-fg">{item.name}</span>
              <Serial value={item.serial_number ?? item.asset_tag} class="self-start" />
            </div>
            <StatusDot {status} class="shrink-0" />
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Remove ${item.name} from cart`}
              onclick={() => cart.remove(item.id)}
            >
              <XIcon aria-hidden="true" />
            </Button>
          </div>
        {/each}
      {/if}
    </div>

    <!-- Right: the commit panel. 244px per §8.4. -->
    <aside class="flex w-full shrink-0 flex-col gap-3 rounded-xl border border-line-strong bg-surface p-(--gutter) lg:w-[244px]">
      <p class="font-medium text-fg">
        {cart.count} {cart.count === 1 ? "item" : "items"}
      </p>

      <DueDatePicker
        value={cart.dueAt}
        disabled={submitting}
        onValueChange={(iso) => cart.setDueAt(iso)}
      />

      {#if session.isAdmin}
        <!-- Check out on behalf of someone else. Admin-only, and the rare
             override it was specced as, not the main path (§16, Q1). -->
        <div class="flex flex-col gap-1.5">
          <Label for="custodian">Custodian</Label>
          <Select.Root type="single" bind:value={custodianId}>
            <Select.Trigger id="custodian" class="w-full">
              {custodianLabel()}
            </Select.Trigger>
            <Select.Content>
              <Select.Item value="">{session.displayName} (you)</Select.Item>
              {#each users as user (user.id)}
                <Select.Item value={user.id}>
                  {[user.first_name, user.last_name].filter(Boolean).join(" ") ||
                    user.student_number}
                </Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
        </div>

        {#if blocked}
          <div class="flex items-start gap-2">
            <Checkbox id="override" bind:checked={overrideOverdue} />
            <Label for="override" class="text-fg-muted">Override the overdue block</Label>
          </div>
        {/if}
      {/if}

      {#if error}
        <p class="text-status-overdue" role="alert">{error}</p>
      {/if}

      <Button
        class="w-full"
        disabled={!canSubmit}
        onclick={() => {
          if (session.isAdmin && blocked && overrideOverdue) overrideOpen = true
          else submit()
        }}
      >
        {#if submitting}
          <LoaderIcon class="animate-spin" aria-hidden="true" />
          Checking out…
        {:else}
          Check out {cart.count}
        {/if}
      </Button>

      {#if !dueAt}
        <p class="text-xs text-fg-faint">Pick a due date to check out.</p>
      {/if}

      <AlertDialog.Root bind:open={clearOpen}>
        <AlertDialog.Trigger>
          {#snippet child({ props })}
            <Button {...props} variant="ghost" class="w-full">
              <TrashIcon aria-hidden="true" />
              Clear cart
            </Button>
          {/snippet}
        </AlertDialog.Trigger>
        <AlertDialog.Content>
          <AlertDialog.Header>
            <AlertDialog.Title>Clear the whole cart?</AlertDialog.Title>
            <AlertDialog.Description>
              {cart.count} {cart.count === 1 ? "item goes" : "items go"} back on the shelf. Nothing
              has been checked out yet, so nothing is undone.
            </AlertDialog.Description>
          </AlertDialog.Header>
          <AlertDialog.Footer>
            <!-- Never let the destructive default take focus (§5.1). -->
            <AlertDialog.Cancel>Keep it</AlertDialog.Cancel>
            <AlertDialog.Action variant="destructive" onclick={() => cart.clear()}>
              Clear cart
            </AlertDialog.Action>
          </AlertDialog.Footer>
        </AlertDialog.Content>
      </AlertDialog.Root>
    </aside>
  </div>
</div>

<!-- ErrConflict: name exactly which items failed, never a bare "conflict". -->
<AlertDialog.Root bind:open={conflictOpen}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>Some items are no longer available</AlertDialog.Title>
      <AlertDialog.Description>{conflictMessage}</AlertDialog.Description>
    </AlertDialog.Header>
    {#if cartItems.unavailableIds.length > 0}
      <ul class="text-fg">
        {#each cartItems.items.filter((i) => i.status !== "available") as item (item.id)}
          <li class="border-b border-line py-1 last:border-b-0">
            {item.name} · {item.serial_number ?? item.asset_tag}
          </li>
        {/each}
      </ul>
    {/if}
    <AlertDialog.Footer>
      <AlertDialog.Cancel>Leave the cart alone</AlertDialog.Cancel>
      <AlertDialog.Action onclick={dropUnavailable}>Remove them and retry</AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>

<!-- The admin override names the overdue items before it happens (§8.4). -->
<AlertDialog.Root bind:open={overrideOpen}>
  <AlertDialog.Content>
    <AlertDialog.Header>
      <AlertDialog.Title>Check out over an overdue block?</AlertDialog.Title>
      <AlertDialog.Description>
        {custodianLabel()} still has these out:
      </AlertDialog.Description>
    </AlertDialog.Header>
    <ul class="text-fg">
      {#each session.overdueItems as row (row.id)}
        <li class="border-b border-line py-1 last:border-b-0">
          {row.asset_name} · {row.serial_number ?? row.asset_tag} ·
          {row.days_overdue} {row.days_overdue === 1 ? "day" : "days"} late
        </li>
      {/each}
    </ul>
    <AlertDialog.Footer>
      <AlertDialog.Cancel>Cancel</AlertDialog.Cancel>
      <AlertDialog.Action
        onclick={() => {
          overrideOpen = false
          submit()
        }}
      >
        Override and check out
      </AlertDialog.Action>
    </AlertDialog.Footer>
  </AlertDialog.Content>
</AlertDialog.Root>
