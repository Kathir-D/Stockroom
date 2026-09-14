<script lang="ts">
	import { cn, type WithElementRef } from "@stockroom/ui/utils";
	import type { HTMLInputAttributes, HTMLInputTypeAttribute } from "svelte/elements";

	type InputType = Exclude<HTMLInputTypeAttribute, "file">;

	type Props = WithElementRef<
		Omit<HTMLInputAttributes, "type"> &
			({ type: "file"; files?: FileList } | { type?: InputType; files?: undefined })
	>;

	let {
		ref = $bindable(null),
		value = $bindable(),
		type,
		files = $bindable(),
		class: className,
		"data-slot": dataSlot = "input",
		...restProps
	}: Props = $props();
</script>

{#if type === "file"}
	<input
		bind:this={ref}
		data-slot={dataSlot}
		class={cn(
			"h-(--control-h) w-full min-w-0 rounded-lg border border-line-control bg-raised px-2.5 text-fg text-[length:var(--text-body)] outline-none transition-colors duration-(--dur-fast) ease-(--ease-brand)",
			"placeholder:text-fg-faint aria-invalid:border-destructive",
			"file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-fg",
			"disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
			className
		)}
		type="file"
		bind:files
		bind:value
		{...restProps}
	/>
{:else}
	<input
		bind:this={ref}
		data-slot={dataSlot}
		class={cn(
			"h-(--control-h) w-full min-w-0 rounded-lg border border-line-control bg-raised px-2.5 text-fg text-[length:var(--text-body)] outline-none transition-colors duration-(--dur-fast) ease-(--ease-brand)",
			"placeholder:text-fg-faint aria-invalid:border-destructive",
			"file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-fg",
			"disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
			className
		)}
		{type}
		bind:value
		{...restProps}
	/>
{/if}
