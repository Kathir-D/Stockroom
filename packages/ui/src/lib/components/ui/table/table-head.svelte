<script lang="ts">
	import { cn, type WithElementRef } from "@stockroom/ui/utils";
	import type { HTMLThAttributes } from "svelte/elements";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLThAttributes> = $props();
</script>

<th bind:this={ref} data-slot="table-head" class={cn(
		"h-(--row-h) px-3 text-left align-middle text-xs font-semibold tracking-wide whitespace-nowrap text-fg-muted uppercase [&:has([role=checkbox])]:pr-0",
		className
	)} {...restProps}>
	{@render children?.()}
</th>
