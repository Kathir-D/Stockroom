<script lang="ts">
  /**
   * The blocking notice shown between sign-in and the browse screen when the
   * user has anything overdue (design-system.md §8.1).
   *
   * It lists the items and their days-late count and has a single **Continue**,
   * because the point is that the person reads it — a toast can vanish before
   * anyone does. The cart and checkout UI disables itself from this moment on,
   * and the server refuses a checkout anyway (§15 Q6).
   */
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Dialog from "@stockroom/ui/components/ui/dialog"
  import type { CustodyRecord } from "../../api/types"
  import { shortDate } from "../../status"
  import Serial from "./serial.svelte"

  let {
    open = $bindable(false),
    items,
    onContinue,
  }: {
    open?: boolean
    items: CustodyRecord[]
    onContinue: () => void
  } = $props()
</script>

<Dialog.Root bind:open>
  <Dialog.Content showCloseButton={false} escapeKeydownBehavior="ignore" interactOutsideBehavior="ignore">
    <Dialog.Header>
      <Dialog.Title class="flex items-center gap-2 text-status-overdue">
        <AlertTriangleIcon class="size-5" aria-hidden="true" />
        {items.length === 1 ? "You have an overdue item" : `You have ${items.length} overdue items`}
      </Dialog.Title>
      <Dialog.Description>
        Bring {items.length === 1 ? "it" : "them"} back before checking anything else out. Scan the
        sticker at this machine to return {items.length === 1 ? "it" : "them"}.
      </Dialog.Description>
    </Dialog.Header>

    <ul>
      {#each items as row (row.id)}
        <li class="flex items-center justify-between gap-3 border-b border-line py-2 last:border-b-0">
          <span class="flex min-w-0 flex-col">
            <span class="truncate text-fg">{row.asset_name}</span>
            <Serial value={row.serial_number ?? row.asset_tag} class="self-start" />
          </span>
          <span class="shrink-0 text-right">
            <span class="block font-semibold text-status-overdue">
              {row.days_overdue} {row.days_overdue === 1 ? "day" : "days"} late
            </span>
            <span class="block text-xs text-fg-faint">was due {shortDate(row.due_at)}</span>
          </span>
        </li>
      {/each}
    </ul>

    <Dialog.Footer>
      <Button onclick={onContinue}>Continue</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
