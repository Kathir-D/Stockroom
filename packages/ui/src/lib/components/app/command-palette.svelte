<script lang="ts">
  /**
   * `Cmd/Ctrl+K`: jump to an asset, or to a screen (design-system.md §5.1).
   *
   * The companion to `/`, which focuses the top-bar search. The two are not the
   * same thing and both exist on purpose: `/` narrows the list you are looking
   * at, this one leaves it. An admin who is four levels into the category tree
   * and needs the overdue list should not have to find the sidebar.
   *
   * **Filtering is ours, not the component's.** `shouldFilter` is off because
   * assets come from `GET /assets?q=`, which has the GIN index and also matches
   * partial identifiers (`T7iB` → `T7iBat-001`) that a client-side substring
   * test over one page of results cannot. The navigation entries are filtered
   * here with a plain `includes`, so both halves of the list answer to the same
   * query.
   *
   * **A scan while this is open still means "track this item".** The palette's
   * input has focus, so the root `<ScanListener>` is deaf to it by design
   * (§9.3) — which would make scanning a camera at this screen do nothing but
   * type a serial into a search box. That is the one thing a barcode must never
   * do (CLAUDE.md §1.5), so the input carries its own scoped listener: a burst
   * that arrives at scanner speed closes the palette and goes to `POST /scan`
   * like any other scan. A burst that was *typed* is left alone, because that is
   * someone searching for a serial by hand and the list already has the answer.
   */
  import CornerDownLeftIcon from "@lucide/svelte/icons/corner-down-left"
  import PackageIcon from "@lucide/svelte/icons/package"
  import { onMount, untrack } from "svelte"
  import * as Command from "@stockroom/ui/components/ui/command"
  import * as api from "../../api/index"
  import type { AssetListItem } from "../../api/types"
  import { attachScanner } from "../../scanner"
  import { resolveStatus } from "../../status"
  import type { Route } from "../../stores/router.svelte"

  let {
    /** Whether an admin's destinations belong in the list. */
    isAdmin = false,
    onNavigate,
    onPickAsset,
    /** A burst that looked like a scanner. The palette closes first. */
    onScan,
  }: {
    isAdmin?: boolean
    onNavigate: (route: Route) => void
    onPickAsset: (asset: AssetListItem) => void
    onScan: (code: string) => void
  } = $props()

  let open = $state(false)
  let query = $state("")
  let input = $state<HTMLInputElement | null>(null)

  let results = $state<AssetListItem[]>([])
  let searching = $state(false)

  /** Matches the browse screen's debounce, so the two feel like one search. */
  const SEARCH_DEBOUNCE_MS = 200
  /** Enough to choose from without turning the palette into the browse list. */
  const MAX_RESULTS = 8

  interface Destination {
    label: string
    /** Extra words the query may match: "who has what" should find Overdue. */
    keywords: string
    route: Route
    adminOnly?: boolean
  }

  const DESTINATIONS: Destination[] = [
    { label: "Browse equipment", keywords: "all inventory catalogue shelf", route: { name: "browse" } },
    { label: "Cart", keywords: "checkout basket due date", route: { name: "cart" } },
    { label: "My history", keywords: "borrowed returned past", route: { name: "history" } },
    { label: "Admin · Assets", keywords: "add edit serial photo", route: { name: "admin", tab: "assets" }, adminOnly: true },
    { label: "Admin · Categories", keywords: "tree type model", route: { name: "admin", tab: "categories" }, adminOnly: true },
    { label: "Admin · Users", keywords: "accounts roster import password", route: { name: "admin", tab: "users" }, adminOnly: true },
    { label: "Admin · Overdue", keywords: "late who has what", route: { name: "admin", tab: "overdue" }, adminOnly: true },
    { label: "Admin · Backup", keywords: "restore archive drive github photos", route: { name: "admin", tab: "backup" }, adminOnly: true },
    { label: "Admin · Settings", keywords: "schedule retention token folder", route: { name: "admin", tab: "settings" }, adminOnly: true },
  ]

  const destinations = $derived(
    DESTINATIONS.filter((d) => {
      if (d.adminOnly && !isAdmin) return false
      const needle = query.trim().toLowerCase()
      if (!needle) return true
      return d.label.toLowerCase().includes(needle) || d.keywords.includes(needle)
    })
  )

  /**
   * Cmd+K on macOS, Ctrl+K elsewhere, and both are accepted on both: the closet
   * PC is Windows and the dev machine is a Mac, and somebody will use the wrong
   * one. `preventDefault` because Ctrl+K is the browser's "focus the address
   * bar" in some builds.
   */
  onMount(() => {
    const onKeydown = (event: KeyboardEvent) => {
      if (event.key !== "k" && event.key !== "K") return
      if (!event.metaKey && !event.ctrlKey) return
      if (event.altKey) return
      event.preventDefault()
      open = !open
    }
    window.addEventListener("keydown", onKeydown)
    return () => window.removeEventListener("keydown", onKeydown)
  })

  // Reset on every open. A palette that reopens holding the last query is one
  // that shows somebody else's search on a shared machine.
  $effect(() => {
    if (open) return
    untrack(() => {
      query = ""
      results = []
      searching = false
    })
  })

  let searchTimer: ReturnType<typeof setTimeout> | null = null
  let searchSeq = 0

  $effect(() => {
    const needle = query.trim()
    if (!open || needle.length < 2) {
      results = []
      searching = false
      return
    }
    searching = true
    if (searchTimer) clearTimeout(searchTimer)
    const seq = ++searchSeq
    searchTimer = setTimeout(async () => {
      try {
        const found = await api.listAssets({ q: needle })
        // A slower earlier request must not overwrite a faster later one: the
        // list would then show results for a query nobody can still see.
        if (seq !== searchSeq) return
        results = found.slice(0, MAX_RESULTS)
      } catch {
        // A failed lookup is an empty list, not an error surface. The palette
        // is a shortcut; every destination in it is reachable another way.
        if (seq === searchSeq) results = []
      } finally {
        if (seq === searchSeq) searching = false
      }
    }, SEARCH_DEBOUNCE_MS)
    return () => {
      if (searchTimer) clearTimeout(searchTimer)
    }
  })

  // The scoped scanner. Attached to the input, so it only ever sees keystrokes
  // aimed at this palette and can never double up with the root listener.
  $effect(() => {
    const target = input
    if (!open || !target) return
    return attachScanner({
      target,
      captureInsideFields: true,
      onBurst: ({ code, fast }) => {
        if (!fast) return
        open = false
        onScan(code)
      },
    })
  })

  function go(route: Route) {
    open = false
    onNavigate(route)
  }

  function pick(asset: AssetListItem) {
    open = false
    onPickAsset(asset)
  }
</script>

<Command.Dialog
  bind:open
  shouldFilter={false}
  title="Jump to"
  description="Search for an item by name or serial, or jump to a screen."
>
  <Command.Input
    bind:ref={input}
    bind:value={query}
    placeholder="Search items by name or serial, or jump to a screen…"
  />
  <Command.List>
    {#if query.trim().length >= 2}
      {#if searching && results.length === 0}
        <Command.Loading>Searching…</Command.Loading>
      {:else if results.length > 0}
        <Command.Group heading="Items">
          {#each results as asset (asset.id)}
            {@const status = resolveStatus(asset)}
            <Command.Item value={asset.id} onSelect={() => pick(asset)}>
              <PackageIcon aria-hidden="true" />
              <span class="truncate">{asset.name}</span>
              {#if asset.serial_number}
                <span class="shrink-0 font-mono text-xs text-fg-faint">
                  {asset.serial_number}
                </span>
              {/if}
              <!-- The status word, never the dot alone (§1.3). Someone jumping
                   to an item wants to know whether it is on the shelf before
                   they walk over to it. -->
              <span class="ml-auto shrink-0 text-xs {status.fg}">{status.label}</span>
            </Command.Item>
          {/each}
        </Command.Group>
      {/if}
    {/if}

    {#if destinations.length > 0}
      <Command.Group heading="Go to">
        {#each destinations as destination (destination.label)}
          <Command.Item value={destination.label} onSelect={() => go(destination.route)}>
            <CornerDownLeftIcon aria-hidden="true" />
            {destination.label}
          </Command.Item>
        {/each}
      </Command.Group>
    {/if}

    {#if destinations.length === 0 && results.length === 0 && !searching}
      <Command.Empty>
        {query.trim().length < 2
          ? "Type at least two characters to search for an item."
          : "Nothing matched. Scanning the sticker works from anywhere."}
      </Command.Empty>
    {/if}
  </Command.List>
</Command.Dialog>
