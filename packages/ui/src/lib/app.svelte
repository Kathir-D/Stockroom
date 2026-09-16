<script lang="ts">
  /**
   * The whole application, in one component, shared by both hosts.
   *
   * `desktop-app/frontend` and `web-app` each render this and nothing else. That
   * is the point of §12's "anything that renders differently in Wails than in the
   * browser is a bug unless the table lists it as deliberate": if the screens
   * lived in either app, the two would drift. The only thing a host passes in is
   * its API base URL.
   *
   * What lives here rather than in a screen, and why:
   *
   *  - **One `<ScanListener>`**, mounted once at the root. Two listeners means
   *    every scan fires twice and the second one is a duplicate check-in (§9.1).
   *  - **Which endpoint a burst hits is decided by the active screen** (§9.2):
   *    while nobody is signed in the sign-in screen owns its own scoped listener
   *    and this one is not mounted at all, so a burst can only ever reach one of
   *    the two.
   *  - **The 401 handler.** An idle timeout can land on any request, and it is
   *    one of exactly two things that empty the cart (§8.4, §15 Q5).
   *  - **Routing.** The cart is a route in this shell and someone can arrive at
   *    it by URL (§8.4).
   */
  import { onMount, untrack } from "svelte"
  import { toast } from "svelte-sonner"
  import { Toaster } from "@stockroom/ui/components/ui/sonner"
  import * as Sheet from "@stockroom/ui/components/ui/sheet"
  import CartDock from "@stockroom/ui/components/app/cart-dock.svelte"
  import OverdueNotice from "@stockroom/ui/components/app/overdue-notice.svelte"
  import ScanListener from "@stockroom/ui/components/app/scan-listener.svelte"
  import ScanResult from "@stockroom/ui/components/app/scan-result.svelte"
  import Sidebar from "@stockroom/ui/components/app/sidebar.svelte"
  import TopBar from "@stockroom/ui/components/app/top-bar.svelte"
  import AdminAssets from "@stockroom/ui/screens/admin/assets.svelte"
  import AdminBackup from "@stockroom/ui/screens/admin/backup.svelte"
  import AdminCategories from "@stockroom/ui/screens/admin/categories.svelte"
  import AdminOverdue from "@stockroom/ui/screens/admin/overdue.svelte"
  import AdminUsers from "@stockroom/ui/screens/admin/users.svelte"
  import Browse from "@stockroom/ui/screens/browse.svelte"
  import CartPage from "@stockroom/ui/screens/cart-page.svelte"
  import History from "@stockroom/ui/screens/history.svelte"
  import SignIn from "@stockroom/ui/screens/sign-in.svelte"
  import * as api from "./api/index"
  import type { AssetDetail, AssetListItem, CheckoutResult } from "./api/types"
  import { attachKeepAlive } from "./keep-alive"
  import { normalizeSerial } from "./scanner"
  import { cart } from "./stores/cart.svelte"
  import { cartItems } from "./stores/cart-items.svelte"
  import { catalog } from "./stores/catalog.svelte"
  import { router } from "./stores/router.svelte"
  import { scanStore } from "./stores/scan.svelte"
  import { session } from "./stores/session.svelte"

  let {
    /** Where the Go server listens. Defaults to CLAUDE.md §9's 127.0.0.1:8080. */
    baseUrl = api.DEFAULT_BASE_URL,
  }: { baseUrl?: string } = $props()

  let overdueOpen = $state(false)
  let sidebarOpen = $state(false)
  /** How many ms to wait after a keystroke before the search hits the server. */
  const SEARCH_DEBOUNCE_MS = 200
  let searchTimer: ReturnType<typeof setTimeout> | null = null

  // Configure before anything can make a request — `session.restore()` below
  // fires on mount and needs the base URL already set. The 401 hook is the idle
  // timeout: `session.clear()` drops the profile, the token *and* the cart.
  // `untrack` because a host passes a constant; re-running this on a prop change
  // would rebuild the handler, not fix anything.
  api.configureApi({
    baseUrl: untrack(() => baseUrl),
    onUnauthorized: () => {
      if (!session.signedIn) return
      session.clear()
      scanStore.clear()
      catalog.reset()
      cartItems.clear()
      toast.info("Signed out after ten minutes idle.")
    },
  })

  onMount(() => {
    const stopRouter = router.start()
    // Keeps the session alive while someone is actually using the app. Most of
    // the browse screen costs no request, so without this a person can be timed
    // out mid-shopping; see lib/keep-alive.ts.
    const stopKeepAlive = attachKeepAlive({
      isSignedIn: () => session.signedIn,
      lastRequestAt: api.lastRequestAt,
      ping: () => api.me(),
    })
    void session.restore()
    return () => {
      stopRouter()
      stopKeepAlive()
    }
  })

  const route = $derived(router.current)
  const signedIn = $derived(session.signedIn && !session.needsPassword)

  /**
   * The scan listener is armed everywhere except the sign-in screen, which owns
   * its own. `captureInsideFields` stays false so typing a search query or a
   * damage note can't fire a phantom scan (§9.3).
   */
  const scannerArmed = $derived(signedIn)

  /** Load the tree once per session; the unit list follows the filter. */
  $effect(() => {
    if (!signedIn) return
    if (catalog.tree.length === 0) void catalog.loadTree().catch(() => {})
  })

  // The browse filter lives in the URL, so a reload keeps the shelf someone was
  // looking at. Category changes reload immediately; search debounces, because
  // every keystroke would otherwise be a request the previous one has to abort.
  $effect(() => {
    if (!signedIn) return
    const category = route.name === "browse" ? route.category : catalog.category
    catalog.category = category
    void catalog.reload()
  })

  $effect(() => {
    if (!signedIn) return
    const term = catalog.search
    if (searchTimer) clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      void term
      void catalog.reload()
    }, SEARCH_DEBOUNCE_MS)
    return () => {
      if (searchTimer) clearTimeout(searchTimer)
    }
  })

  function selectCategory(id: string | undefined) {
    sidebarOpen = false
    router.go({ name: "browse", category: id })
  }

  function onSignedIn() {
    // The overdue warning is blocking and comes before the browse screen (§8.1).
    overdueOpen = session.hasOverdue
    router.go({ name: "browse", category: catalog.category })
  }

  async function signOut() {
    await session.signOut()
    scanStore.clear()
    catalog.reset()
    cartItems.clear()
    router.go({ name: "browse" })
  }

  /**
   * Every item barcode, from anywhere in the app.
   *
   * `POST /scan` decides what a serial means — a checked-out item is checked in
   * before this resolves, an available one comes back as the detail payload. The
   * frontend never branches on the code itself (CLAUDE.md §10).
   */
  async function onBurst({ code }: { code: string }) {
    const serial = normalizeSerial(code)
    if (!serial) return
    try {
      const result = await api.scan(serial)
      scanStore.show({ kind: "scan", result })
      if (result.action === "checked_in") {
        // The list and the viewer's own overdue flag both just changed.
        catalog.patchUnit(result.asset)
        void session.refresh()
      } else {
        catalog.highlightUnit(result.asset.id)
      }
    } catch (error) {
      if (error instanceof api.ApiError && error.status === 404) {
        scanStore.show({ kind: "unknown", code: serial })
        return
      }
      scanStore.show({
        kind: "error",
        code: serial,
        message: error instanceof Error ? error.message : String(error),
      })
    }
  }

  function addToCart(unit: AssetListItem) {
    if (session.checkoutBlocked) return
    if (cart.add(unit.id)) return
    toast.info(`${unit.name} is already in the cart.`)
  }

  /**
   * The other half of the Add button. Every surface that can put a unit in the
   * cart can take it back out with the same press, so undo is where the action
   * was rather than only on the cart page.
   */
  function removeFromCart(unit: AssetListItem) {
    cart.remove(unit.id)
  }

  async function checkInFromDetail(asset: AssetDetail, note: string) {
    const result = await api.checkIn(asset.id, note)
    catalog.patchUnit(result.asset)
    toast.success(`${result.asset.name} checked in`)
    await session.refresh()
  }

  /**
   * The damage note on the scan confirmation surface.
   *
   * A scanned checked-out item is checked in before the surface renders, so
   * there is no `checkIn` call left to hand the note to — it goes to the custody
   * event directly, landing on the same `condition_in` column (§8.6).
   */
  async function saveDamageNote(custodyEventId: string, note: string) {
    await api.annotateCustody(custodyEventId, note)
  }

  function onCheckedOut(result: CheckoutResult) {
    cart.clear()
    cartItems.clear()
    scanStore.recordCheckout(result)
    scanStore.show({ kind: "checkout", result })
    void catalog.reload()
    router.backToBrowse()
  }
</script>

<Toaster position="bottom-center" />

{#if !session.ready}
  <!-- A blank frame rather than a spinner: /me settles in a few ms on localhost
       and a flash of loading state is worse than a beat of nothing. -->
  <div class="min-h-screen bg-ground"></div>
{:else if !signedIn}
  <SignIn {onSignedIn} />
{:else}
  <ScanListener onBurst={(burst) => onBurst(burst)} />

  <div class="flex h-screen flex-col bg-ground text-fg" data-density="comfortable">
    <TopBar
      path={catalog.selectedPath}
      bind:search={catalog.search}
      scannerReady={scannerArmed}
      onSelectCategory={selectCategory}
      onOpenSidebar={() => (sidebarOpen = true)}
      onHistory={() => router.go({ name: "history" })}
      onSignOut={signOut}
    />

    <div class="flex min-h-0 flex-1">
      <!-- 236px fixed, and a sheet below 720px (§7.2). Same markup either way. -->
      <aside class="hidden w-[236px] shrink-0 border-r border-line lg:block">
        <Sidebar
          tree={catalog.tree}
          selectedCategory={catalog.category}
          {route}
          isAdmin={session.isAdmin}
          openPath={catalog.selectedPath.map((n) => n.id)}
          onSelectCategory={selectCategory}
          onNavigate={(next) => router.go(next)}
        />
      </aside>

      <Sheet.Root bind:open={sidebarOpen}>
        <Sheet.Content side="left" class="w-[min(300px,85vw)] p-0">
          <Sheet.Header class="sr-only">
            <Sheet.Title>Categories</Sheet.Title>
          </Sheet.Header>
          <Sidebar
            tree={catalog.tree}
            selectedCategory={catalog.category}
            {route}
            isAdmin={session.isAdmin}
            openPath={catalog.selectedPath.map((n) => n.id)}
            onSelectCategory={selectCategory}
            onNavigate={(next) => {
              sidebarOpen = false
              router.go(next)
            }}
          />
        </Sheet.Content>
      </Sheet.Root>

      <main class="min-w-0 flex-1 overflow-y-auto">
        <div class="mx-auto max-w-[1440px] p-(--gutter)">
          {#if route.name === "cart"}
            <CartPage onBack={() => router.backToBrowse()} {onCheckedOut} />
          {:else if route.name === "history"}
            <History />
          {:else if route.name === "admin" && session.isAdmin}
            {#if route.tab === "assets"}
              <AdminAssets />
            {:else if route.tab === "categories"}
              <AdminCategories />
            {:else if route.tab === "users"}
              <AdminUsers />
            {:else if route.tab === "overdue"}
              <AdminOverdue />
            {:else}
              <AdminBackup />
            {/if}
          {:else}
            <Browse onAdd={addToCart} onRemove={removeFromCart} onCheckIn={checkInFromDetail} />
          {/if}
        </div>
      </main>
    </div>

    <!-- Absent at zero items, and absent on the cart page: two `Check out`
         buttons in one view is one too many (§8.4). -->
    {#if !cart.isEmpty && route.name !== "cart"}
      <CartDock
        knownAssets={catalog.units}
        overdueItems={session.overdueItems}
        isAdmin={session.isAdmin}
        onReview={() => router.go({ name: "cart" })}
        onOverride={() => router.go({ name: "cart" })}
      />
    {/if}
  </div>

  <ScanResult
    viewerId={session.profile?.id ?? null}
    canAdd={!session.checkoutBlocked}
    inCart={scanStore.surface?.kind === "scan" ? cart.has(scanStore.surface.result.asset.id) : false}
    onAdd={addToCart}
    onRemove={removeFromCart}
    onSaveNote={saveDamageNote}
    onSignOut={signOut}
  />

  <OverdueNotice
    bind:open={overdueOpen}
    items={session.overdueItems}
    onContinue={() => (overdueOpen = false)}
  />
{/if}
