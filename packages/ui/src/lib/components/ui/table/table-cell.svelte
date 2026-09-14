<script lang="ts">
	import { cn, type WithElementRef } from "@stockroom/ui/utils";
	import type { HTMLTdAttributes } from "svelte/elements";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLTdAttributes> = $props();
</script>

<td bind:this={ref} data-slot="table-cell" class={cn(
		"h-(--row-h) px-3 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0",
		className
	)} {...restProps}>
	{@render children?.()}
</td>
