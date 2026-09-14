<script lang="ts">
	import { cn, type WithElementRef } from "@stockroom/ui/utils";
	import type { HTMLAttributes } from "svelte/elements";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLTableRowElement>> = $props();
</script>

<tr
	bind:this={ref}
	data-slot="table-row"
	class={cn(
			"min-h-(--row-h) border-b border-line transition-colors duration-(--dur-fast) ease-(--ease-brand)",
			"hover:bg-raised data-[state=selected]:bg-raised has-aria-expanded:bg-raised",
			className
		)}
	{...restProps}
>
	{@render children?.()}
</tr>
