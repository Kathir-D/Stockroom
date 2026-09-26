<script lang="ts">
  /**
   * Admin → Activity (ROADMAP §2.5): one timeline of everything that happened
   * at the closet, newest first.
   *
   * Its job is the question an admin has when something is missing without
   * having been checked out: who was in the closet, who signed in, what was
   * scanned, what went out and came back. So the camera's visits sit in the
   * same list as the sign-ins, scans and checkouts that happened during them,
   * each visit with its snapshot and a play button, and the admin draws the
   * conclusion -- nothing here links a person on camera to an account
   * (ROADMAP §2.6).
   *
   * The filters live in the URL (`#/admin/activity?from=…`), so an item's
   * "closet activity since it was returned" link lands here already filtered,
   * and a reload keeps the view. Admin-only, and the server refuses everyone
   * else; nothing here is a permission check.
   */
  import CameraIcon from "@lucide/svelte/icons/camera"
  import DownloadIcon from "@lucide/svelte/icons/download"
  import PlayIcon from "@lucide/svelte/icons/play"
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw"
  import ScanLineIcon from "@lucide/svelte/icons/scan-line"
  import BookmarkIcon from "@lucide/svelte/icons/bookmark"
  import { toast } from "svelte-sonner"
  import { Button } from "@stockroom/ui/components/ui/button"
  import { Input } from "@stockroom/ui/components/ui/input"
  import { Label } from "@stockroom/ui/components/ui/label"
  import * as Select from "@stockroom/ui/components/ui/select"
  import { Skeleton } from "@stockroom/ui/components/ui/skeleton"
  import EmptyState from "@stockroom/ui/components/app/empty-state.svelte"
  import VisitSnapshot from "@stockroom/ui/components/app/visit-snapshot.svelte"
  import VisitPlayerDialog from "@stockroom/ui/components/app/visit-player-dialog.svelte"
  import * as api from "../../api/index"
  import type {
    ActivityCategory,
    ActivityEntry,
    ActivityQuery,
    CameraOverview,
    ClosetVisit,
    Profile,
  } from "../../api/types"
  import { saveBlob } from "../../download"
  import { router } from "../../stores/router.svelte"

  const TYPES: { value: ActivityCategory; label: string }[] = [
    { value: "closet", label: "Closet" },
    { value: "account", label: "Sign-ins" },
    { value: "scan", label: "Scans" },
    { value: "equipment", label: "Equipment" },
    { value: "admin", label: "Admin" },
  ]

  /** The URL's query, which is the filter. */
  const query = $derived(
    router.current.name === "admin" ? (router.current.query ?? {}) : ({} as Record<string, string>),
  )

  // The form mirrors the query; Apply writes it back to the URL.
  let from = $state("")
  let to = $state("")
  let person = $state("")
  let q = $state("")
  let types = $state<ActivityCategory[]>([])
  let scansOnly = $state(false)

  $effect(() => {
    from = toLocalInput(query.from)
    to = toLocalInput(query.to)
    person = query.person ?? ""
    q = query.q ?? ""
    types = (query.type ?? "").split(",").filter(Boolean) as ActivityCategory[]
    scansOnly = query.scans === "1"
  })

  let entries = $state<ActivityEntry[]>([])
  let more = $state(false)
  let loading = $state(true)
  let loadingMore = $state(false)
  let error = $state<string | null>(null)
  let users = $state<Profile[]>([])
  let cam = $state<CameraOverview | null>(null)
  let playing = $state<ClosetVisit | null>(null)
  let playerOpen = $state(false)

  function toLocalInput(iso: string | undefined): string {
    if (!iso) return ""
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return ""
    const pad = (n: number) => String(n).padStart(2, "0")
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
  }

  function fromLocalInput(v: string): string | undefined {
    if (!v) return undefined
    const d = new Date(v)
    return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
  }

  function apiQuery(extra: Partial<ActivityQuery> = {}): ActivityQuery {
    return {
      from: query.from,
      to: query.to,
      person: query.person,
      item: query.item,
      type: query.type,
      scans: query.scans === "1" ? "1" : undefined,
      q: query.q,
      ...extra,
    }
  }

  // Every request takes a number; an answer that is not the latest one is
  // dropped, so changing a filter mid-request never mixes two filters' rows.
  let seq = 0

  async function load() {
    const mine = ++seq
    loading = true
    error = null
    try {
      const page = await api.activity(apiQuery())
      if (mine !== seq) return
      entries = page.entries
      more = page.more
    } catch (err) {
      if (mine === seq) error = err instanceof Error ? err.message : String(err)
    } finally {
      if (mine === seq) loading = false
    }
  }

  async function loadMore() {
    const last = entries.at(-1)
    if (!last) return
    const mine = ++seq
    loadingMore = true
    try {
      const page = await api.activity(apiQuery({ before: last.at, before_id: last.id }))
      if (mine !== seq) return
      entries = [...entries, ...page.entries]
      more = page.more
    } catch (err) {
      if (mine === seq) toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      loadingMore = false
    }
  }

  $effect(() => {
    void query
    load()
  })

  $effect(() => {
    api.listUsers().then((u) => (users = u), () => {})
    api.camera().then((c) => (cam = c), () => {})
  })

  function apply(event?: SubmitEvent) {
    event?.preventDefault()
    const next: Record<string, string> = {}
    const f = fromLocalInput(from)
    const t = fromLocalInput(to)
    if (f) next.from = f
    if (t) next.to = t
    if (person) next.person = person
    if (query.item) next.item = query.item
    if (types.length) next.type = types.join(",")
    if (scansOnly) next.scans = "1"
    if (q.trim()) next.q = q.trim()
    router.go({ name: "admin", tab: "activity", query: next })
  }

  function clear() {
    router.go({ name: "admin", tab: "activity" })
  }

  function toggleType(t: ActivityCategory) {
    types = types.includes(t) ? types.filter((x) => x !== t) : [...types, t]
  }

  async function exportCsv() {
    try {
      const blob = await api.activityCsv(apiQuery())
      saveBlob(blob, `stockroom-activity-${new Date().toISOString().slice(0, 10)}.csv`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  function play(visit: ClosetVisit) {
    playing = visit
    playerOpen = true
  }

  function visitChanged(v: ClosetVisit) {
    playing = v
    entries = entries.map((e) => (e.visit?.id === v.id ? { ...e, visit: v } : e))
  }

  const personLabel = $derived(() => {
    if (!person) return "Anyone"
    const u = users.find((x) => x.id === person)
    return u ? [u.first_name, u.last_name].filter(Boolean).join(" ") || u.student_number : "Someone"
  })

  /** The timeline, grouped by local day. */
  const days = $derived.by(() => {
    const out: { day: string; rows: ActivityEntry[] }[] = []
    for (const e of entries) {
      const day = new Date(e.at).toLocaleDateString(undefined, {
        weekday: "long",
        month: "long",
        day: "numeric",
        year: "numeric",
      })
      const last = out.at(-1)
      if (last && last.day === day) last.rows.push(e)
      else out.push({ day, rows: [e] })
    }
    return out
  })

  function time(iso: string) {
    return new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" })
  }

  function duration(s: number | null | undefined) {
    if (s == null) return ""
    if (s < 60) return `${s} s`
    const m = Math.floor(s / 60)
    if (m < 60) return `${m} min ${s % 60} s`
    return `${Math.floor(m / 60)} h ${m % 60} min`
  }

  const CATEGORY_LABEL: Record<ActivityCategory, string> = {
    closet: "Closet",
    account: "Sign-in",
    scan: "Scan",
    equipment: "Equipment",
    admin: "Admin",
  }

  const cameraLine = $derived.by(() => {
    if (!cam || !cam.settings.enabled) return null
    const s = cam.status
    switch (s.state) {
      case "online":
        return `Camera online · ${s.in_closet} in the closet now · ${s.visits_today} visit${s.visits_today === 1 ? "" : "s"} today`
      case "starting":
        return "Camera starting…"
      default:
        return s.message
    }
  })
</script>

<div data-density="compact" class="flex flex-col gap-3">
  <div class="flex flex-wrap items-center gap-3">
    <div class="flex flex-col">
      <h1 class="text-base font-semibold text-fg">Activity</h1>
      <p class="text-xs text-fg-muted">
        Everything that happened at the closet, newest first: the camera's visits, sign-ins, every scan,
        checkouts and returns, and admin changes.
      </p>
    </div>
    <span class="flex-1"></span>
    {#if cameraLine}
      <span class="flex items-center gap-1.5 text-xs text-fg-muted">
        <CameraIcon
          class="size-3.5 {cam?.status.state === 'online' ? 'text-status-available' : 'text-status-due-soon'}"
          aria-hidden="true"
        />
        {cameraLine}
      </span>
    {/if}
    <Button variant="secondary" size="sm" onclick={exportCsv}>
      <DownloadIcon aria-hidden="true" /> Export CSV
    </Button>
    <Button variant="ghost" size="icon-sm" aria-label="Refresh the timeline" onclick={load}>
      <RefreshIcon aria-hidden="true" />
    </Button>
  </div>

  <form
    class="flex flex-wrap items-end gap-3 rounded-xl border border-line-strong bg-surface p-3"
    onsubmit={apply}
  >
    <div class="flex flex-col gap-1">
      <Label for="act-from">From</Label>
      <Input id="act-from" type="datetime-local" bind:value={from} class="w-52" />
    </div>
    <div class="flex flex-col gap-1">
      <Label for="act-to">To</Label>
      <Input id="act-to" type="datetime-local" bind:value={to} class="w-52" />
    </div>
    <div class="flex flex-col gap-1">
      <Label for="act-person">Person</Label>
      <Select.Root type="single" bind:value={person}>
        <Select.Trigger id="act-person" class="w-48">{personLabel()}</Select.Trigger>
        <Select.Content>
          <Select.Item value="">Anyone</Select.Item>
          {#each users as u (u.id)}
            <Select.Item value={u.id}>
              {[u.first_name, u.last_name].filter(Boolean).join(" ") || u.student_number}
            </Select.Item>
          {/each}
        </Select.Content>
      </Select.Root>
    </div>
    <div class="flex flex-col gap-1">
      <Label for="act-q">Search</Label>
      <Input id="act-q" bind:value={q} placeholder="Serial, name, code…" class="w-48" />
    </div>
    <fieldset class="flex flex-col gap-1">
      <legend class="mb-1 text-sm font-medium text-fg">Type</legend>
      <div class="flex flex-wrap gap-1">
        {#each TYPES as t (t.value)}
          <Button
            type="button"
            size="sm"
            variant={types.includes(t.value) ? "default" : "secondary"}
            aria-pressed={types.includes(t.value)}
            onclick={() => toggleType(t.value)}
          >
            {t.label}
          </Button>
        {/each}
        <Button
          type="button"
          size="sm"
          variant={scansOnly ? "default" : "secondary"}
          aria-pressed={scansOnly}
          onclick={() => (scansOnly = !scansOnly)}
          title="Everything a barcode scan caused, whatever it did"
        >
          <ScanLineIcon aria-hidden="true" /> By scanner
        </Button>
      </div>
    </fieldset>
    <span class="flex-1"></span>
    <Button type="button" variant="ghost" size="sm" onclick={clear}>Clear</Button>
    <Button type="submit" size="sm">Apply</Button>
  </form>

  {#if query.item}
    <p class="text-xs text-fg-muted">
      Showing one item's rows only.
      <button type="button" class="underline" onclick={() => router.go({ name: "admin", tab: "activity", query: { ...query, item: "" } })}>
        Show everything in this range
      </button>
    </p>
  {/if}

  {#if error}
    <EmptyState title="Couldn't load the activity log" description={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={load}>Try again</Button>
      {/snippet}
    </EmptyState>
  {:else if loading}
    <div class="flex flex-col gap-px" aria-busy="true" aria-label="Loading activity">
      {#each Array(6) as _, index (index)}
        <Skeleton class="h-(--row-h) rounded-none" />
      {/each}
    </div>
  {:else if entries.length === 0}
    <EmptyState title="Nothing here" description="No activity matches these filters." />
  {:else}
    <div class="flex flex-col gap-4">
      {#each days as group (group.day)}
        <section class="flex flex-col gap-1">
          <h2 class="sticky top-0 z-10 bg-ground py-1 text-xs font-semibold uppercase tracking-wide text-fg-muted">
            {group.day}
          </h2>
          <ol class="overflow-hidden rounded-xl border border-line-strong">
            {#each group.rows as e (e.id)}
              <li class="flex items-center gap-3 border-b border-line bg-surface px-3 py-2 last:border-b-0">
                <span class="w-20 shrink-0 tabular-nums text-xs text-fg-muted">{time(e.at)}</span>
                <span
                  class="w-20 shrink-0 text-xs font-medium {e.category === 'closet' ? 'text-status-out' : 'text-fg-muted'}"
                >
                  {CATEGORY_LABEL[e.category]}
                </span>
                {#if e.visit && e.action === "walked_in"}
                  <VisitSnapshot visitId={e.visit.id} available={e.visit.has_snapshot} />
                {/if}
                <div class="flex min-w-0 flex-1 flex-col">
                  <span class="text-sm text-fg">
                    {#if e.via_scanner}
                      <ScanLineIcon class="mr-1 inline size-3.5 text-fg-muted" aria-label="By scanner" />
                    {/if}
                    {e.summary || e.action}
                  </span>
                  <span class="truncate text-xs text-fg-muted">
                    {[e.actor_name, e.asset_label, e.details?.screen ? `on ${e.details.screen}` : ""]
                      .filter(Boolean)
                      .join(" · ")}
                    {#if e.visit && e.action === "walked_in"}
                      {e.visit.ended_at
                        ? `In the closet for ${duration(e.visit.duration_seconds)}`
                        : "Still in the closet"}
                    {/if}
                  </span>
                </div>
                {#if e.visit && e.action === "walked_in"}
                  {#if e.visit.keep}
                    <BookmarkIcon class="size-4 text-fg" aria-label="Kept" />
                  {/if}
                  {#if e.visit.has_clip}
                    <Button variant="secondary" size="sm" onclick={() => e.visit && play(e.visit)}>
                      <PlayIcon aria-hidden="true" /> Play
                    </Button>
                  {:else if e.visit.recording_deleted}
                    <span class="text-xs text-fg-faint">Recording deleted</span>
                  {:else if e.visit.ended_at}
                    <span class="text-xs text-fg-faint">Saving recording…</span>
                  {/if}
                {/if}
              </li>
            {/each}
          </ol>
        </section>
      {/each}
      {#if more}
        <Button variant="secondary" disabled={loadingMore} onclick={loadMore} class="self-center">
          {loadingMore ? "Loading…" : "Load older"}
        </Button>
      {/if}
    </div>
  {/if}
</div>

<VisitPlayerDialog bind:open={playerOpen} visit={playing} onKeepChanged={visitChanged} />
