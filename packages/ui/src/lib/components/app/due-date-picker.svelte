<script lang="ts">
  /**
   * The due-date picker: `popover` + `calendar`, hard-capped at seven days
   * (design-system.md §5.1).
   *
   * Everything beyond the cap is *disabled* rather than validated after the
   * fact, and the hint names the last acceptable date. The server refuses a
   * late `due_at` regardless — the cap is enforced in both places, like the
   * overdue block — but a calendar that offers a date it will then reject is a
   * calendar nobody trusts.
   */
  import CalendarIcon from "@lucide/svelte/icons/calendar"
  import { CalendarDate, type DateValue, getLocalTimeZone, today } from "@internationalized/date"
  import { Calendar } from "@stockroom/ui/components/ui/calendar"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Popover from "@stockroom/ui/components/ui/popover"
  import { MAX_CHECKOUT_DAYS } from "../../api/index"
  import { capHint, dueInstantFor, isSelectableDueDate } from "../../due"
  import { shortDate } from "../../status"

  let {
    /** RFC 3339, or null until a date is picked. Read-only. */
    value = null,
    disabled = false,
    onValueChange,
  }: {
    value?: string | null
    disabled?: boolean
    /**
     * Callback rather than `bind:value`, because the caller that owns this value
     * is the cart store, which persists it to sessionStorage on the way through.
     * A direct binding would write the field and skip the persistence.
     */
    onValueChange: (iso: string | null) => void
  } = $props()

  const zone = getLocalTimeZone()

  function toCalendarDate(iso: string | null): DateValue | undefined {
    if (!iso) return undefined
    const date = new Date(iso)
    if (Number.isNaN(date.getTime())) return undefined
    return new CalendarDate(date.getFullYear(), date.getMonth() + 1, date.getDate())
  }

  // Derived, not $state seeded once: the cart store owns `value` and can change
  // it while this is mounted (a cleared cart resets the due date), and a local
  // copy left the calendar highlighting a day the button no longer named.
  const selected = $derived(toCalendarDate(value))
  let open = $state(false)

  const minDate = $derived(today(zone))
  const maxDate = $derived(today(zone).add({ days: MAX_CHECKOUT_DAYS }))

  function onSelect(next: DateValue | undefined) {
    // The selection travels out through `onValueChange` and comes back in as
    // `value`; there is nothing to assign here.
    onValueChange(next ? dueInstantFor(next.toDate(zone)) : null)
    if (next) open = false
  }
</script>

<div class="flex flex-col gap-1.5">
  <Popover.Root bind:open>
    <Popover.Trigger>
      {#snippet child({ props })}
        <Button {...props} variant="secondary" {disabled} class="w-full justify-start">
          <CalendarIcon aria-hidden="true" />
          {value ? `Due ${shortDate(value)}` : "Pick a due date"}
        </Button>
      {/snippet}
    </Popover.Trigger>
    <Popover.Content class="w-auto p-0">
      <Calendar
        type="single"
        value={selected}
        onValueChange={onSelect}
        minValue={minDate}
        maxValue={maxDate}
        isDateUnavailable={(date: DateValue) => !isSelectableDueDate(date.toDate(zone))}
      />
    </Popover.Content>
  </Popover.Root>

  <p class="text-xs text-fg-faint">
    {MAX_CHECKOUT_DAYS} days maximum — latest is {capHint()}.
  </p>
</div>
