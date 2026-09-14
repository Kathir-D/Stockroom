<script lang="ts">
	import { cn, type WithElementRef } from "@stockroom/ui/utils";
	import type { HTMLAttributes } from "svelte/elements";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLTableSectionElement>> = $props();
</script>

<thead
	bind:this={ref}
	data-slot="table-header"
	class={cn("sticky top-0 z-10 bg-surface [&_tr]:border-b [&_tr]:border-line", className)}
	{...restProps}
>
	{@render children?.()}
</thead>
