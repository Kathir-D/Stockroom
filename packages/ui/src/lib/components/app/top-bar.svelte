<script lang="ts">
  /**
   * The 56px top bar (design-system.md §7.1).
   *
   * Breadcrumb of the active category path on the left; search, the scanner
   * indicator and the signed-in user on the right. The user chip carries the
   * first name, the student number in mono and an overdue indicator when
   * `Me().has_overdue` is true.
   *
   * The scanner indicator is here because of §9.5: when someone scans and
   * nothing happens, the first question is always "is it even listening?".
   * It is a state readout, not decoration, which is why it may carry a hue.
   */
  import MenuIcon from "@lucide/svelte/icons/menu"
  import ScanLineIcon from "@lucide/svelte/icons/scan-line"
  import SearchIcon from "@lucide/svelte/icons/search"
  import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert"
  import LogOutIcon from "@lucide/svelte/icons/log-out"
  import ClockIcon from "@lucide/svelte/icons/history"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Input } from "@stockroom/ui/components/ui/input"
  import * as DropdownMenu from "@stockroom/ui/components/ui/dropdown-menu"
  import * as Tooltip from "@stockroom/ui/components/ui/tooltip"
  import { cn } from "@stockroom/ui/utils"
  import type { CategoryNode } from "../../api/types"
  import { session } from "../../stores/session.svelte"

  let {
    /** Root-first path to the selected category, for the breadcrumb. */
    path = [],
    search = $bindable(""),
    /** Whether the root `<ScanListener>` is currently armed for item scans. */
    scannerReady = true,
    onSelectCategory,
    onOpenSidebar,
    onHistory,
    onSignOut,
  }: {
    path?: CategoryNode[]
    search?: string
    scannerReady?: boolean
    onSelectCategory: (id: string | undefined) => void
    onOpenSidebar?: () => void
    onHistory: () => void
    onSignOut: () => void
  } = $props()

  let searchInput = $state<HTMLInputElement | null>(null)

  /**
   * `/` focuses search (§10). Bound here rather than in the shell so the key and
   * the field it jumps to live in one file; ignored while focus is already in a
   * text field, or typing a slash into a damage note would steal it.
   */
  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return
    const active = document.activeElement
    if (
      active instanceof HTMLElement &&
      (active.isContentEditable ||
        active.tagName === "INPUT" ||
        active.tagName === "TEXTAREA")
    )
      return
    event.preventDefault()
    searchInput?.focus()
  }
</script>

<svelte:window onkeydown={onKeydown} />

<header
  class="flex h-14 shrink-0 items-center gap-3 border-b border-line bg-surface px-(--gutter)"
>
  {#if onOpenSidebar}
    <Button
      variant="ghost"
      size="icon-sm"
      class="lg:hidden"
      aria-label="Open the category sidebar"
      onclick={onOpenSidebar}
    >
      <MenuIcon aria-hidden="true" />
    </Button>
  {/if}

  <!-- Breadcrumb. Each hop is a filter, so each hop is a button. -->
  <nav aria-label="Category" class="flex min-w-0 items-center gap-1 text-sm">
    <button
      type="button"
      class="rounded-sm px-1.5 py-1 text-fg-muted transition-colors duration-(--dur-fast) hover:text-fg"
      onclick={() => onSelectCategory(undefined)}
    >
      All equipment
    </button>
    {#each path as node, index (node.id)}
      <span class="text-fg-faint" aria-hidden="true">/</span>
      <button
        type="button"
        class={cn(
          "truncate rounded-sm px-1.5 py-1 transition-colors duration-(--dur-fast) hover:text-fg",
          index === path.length - 1 ? "text-fg" : "text-fg-muted"
        )}
        aria-current={index === path.length - 1 ? "page" : undefined}
        onclick={() => onSelectCategory(node.id)}
      >
        {node.name}
      </button>
    {/each}
  </nav>

  <span class="flex-1"></span>

  <div class="relative w-64 max-w-[40vw]">
    <SearchIcon
      class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-fg-faint"
      aria-hidden="true"
    />
    <Input
      bind:ref={searchInput}
      bind:value={search}
      placeholder="Search  /"
      aria-label="Search equipment"
      class="pl-8"
    />
  </div>

  <!-- §9.5: the answer to "is it even listening?" -->
  <Tooltip.Provider>
    <Tooltip.Root>
      <Tooltip.Trigger>
        {#snippet child({ props })}
          <span
            {...props}
            class={cn(
              "flex size-8 items-center justify-center rounded-(--radius)",
              scannerReady ? "text-status-available" : "text-fg-faint"
            )}
          >
            <ScanLineIcon class="size-4" aria-hidden="true" />
            <span class="sr-only">
              {scannerReady ? "Scanner listening" : "Scanner paused"}
            </span>
          </span>
        {/snippet}
      </Tooltip.Trigger>
      <Tooltip.Content>
        {scannerReady
          ? "Listening for item barcodes"
          : "Paused while a text field has focus"}
      </Tooltip.Content>
    </Tooltip.Root>
  </Tooltip.Provider>

  <DropdownMenu.Root>
    <DropdownMenu.Trigger>
      {#snippet child({ props })}
        <button
          {...props}
          class="flex min-h-(--tap) items-center gap-2 rounded-(--radius) px-2 text-left transition-colors duration-(--dur-fast) hover:bg-raised"
        >
          <span class="flex flex-col leading-tight">
            <span class="text-sm text-fg">{session.displayName}</span>
            {#if session.profile?.student_number}
              <span class="font-mono text-[11px] text-fg-faint">
                {session.profile.student_number}
              </span>
            {/if}
          </span>
          {#if session.hasOverdue}
            <AlertTriangleIcon
              class="size-4 text-status-overdue"
              aria-label="You have an overdue item"
            />
          {/if}
        </button>
      {/snippet}
    </DropdownMenu.Trigger>
    <DropdownMenu.Content align="end">
      <DropdownMenu.Item onSelect={onHistory}>
        <ClockIcon aria-hidden="true" />
        My history
      </DropdownMenu.Item>
      <DropdownMenu.Separator />
      <DropdownMenu.Item onSelect={onSignOut}>
        <LogOutIcon aria-hidden="true" />
        Sign out
      </DropdownMenu.Item>
    </DropdownMenu.Content>
  </DropdownMenu.Root>
</header>
