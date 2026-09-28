<script lang="ts">
  /**
   * The due-date picker: `popover` + `calendar`, capped at the checkout days
   * an admin set, seven by default (design-system.md §5.1).
   *
   * The person picks the **last day they need the item**. It is due at the
   * closing time on the next school day after that (CLAUDE.md §7, `due.ts`),
   * and the button says exactly when.
   *
   * Everything beyond the cap is *disabled* rather than validated after the
   * fact, and the hint names the last acceptable date. The server refuses a
   * late `due_at` regardless — the cap is enforced in both places, like the
   * overdue block — but a calendar that offers a date it will then reject is a
   * calendar nobody trusts.
   */
  import CalendarIcon from "@lucide/svelte/icons/calendar"
  import { type DateValue, getLocalTimeZone, today } from "@internationalized/date"
  import { Calendar } from "@stockroom/ui/components/ui/calendar"
  import { Button } from "@stockroom/ui/components/ui/button"
  import * as Popover from "@stockroom/ui/components/ui/popover"
  import { dueInstantFor, dueLabel, isSelectableLastDay, latestDue } from "../../due"
  import { rules } from "../../stores/rules.svelte"

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

  /**
   * The day picked, kept here because the due instant cannot be turned back
   * into it: a Friday, a Saturday and a Sunday are all due on Monday. Cleared
   * when the cart clears the due date.
   */
  let picked = $state<DateValue | undefined>(undefined)
  $effect(() => {
    if (value === null) picked = undefined
  })
  let open = $state(false)

  const minDate = $derived(today(zone))
  const maxDate = $derived(today(zone).add({ days: rules.maxCheckoutDays }))

  function onSelect(next: DateValue | undefined) {
    picked = next
    // The selection travels out through `onValueChange` and comes back in as
    // `value`; there is nothing else to assign here.
    onValueChange(next ? dueInstantFor(next.toDate(zone), rules.dueTime, rules.closedDates) : null)
    if (next) open = false
  }
</script>

<div class="flex flex-col gap-1.5">
  <Popover.Root bind:open>
    <Popover.Trigger>
      {#snippet child({ props })}
        <Button {...props} variant="secondary" {disabled} class="w-full justify-start">
          <CalendarIcon aria-hidden="true" />
          {value ? `Due back ${dueLabel(value)}` : "Pick the last day you need it"}
        </Button>
      {/snippet}
    </Popover.Trigger>
    <Popover.Content class="w-auto p-0">
      <Calendar
        type="single"
        value={picked}
        onValueChange={onSelect}
        minValue={minDate}
        maxValue={maxDate}
        isDateUnavailable={(date: DateValue) =>
          !isSelectableLastDay(date.toDate(zone), rules.maxCheckoutDays)}
      />
    </Popover.Content>
  </Popover.Root>

  <p class="text-xs text-fg-faint">
    Due back at {rules.dueTime} on the next school day after the day you pick. Up to
    {rules.maxCheckoutDays} days; the latest is {dueLabel(latestDue(rules.maxCheckoutDays, rules.dueTime, rules.closedDates))}.
  </p>
</div>
