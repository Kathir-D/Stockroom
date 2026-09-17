<script lang="ts">
	import * as Dialog from "@stockroom/ui/components/ui/dialog";
	import { cn, type WithoutChildrenOrChild } from "@stockroom/ui/utils";
	import Command from "./command.svelte";
	import type { Command as CommandPrimitive, Dialog as DialogPrimitive } from "bits-ui";
	import type { Snippet } from "svelte";

	let {
		open = $bindable(false),
		ref = $bindable(null),
		value = $bindable(""),
		title = "Command Palette",
		description = "Search for a command to run...",
		showCloseButton = false,
		portalProps,
		children,
		class: className,
		...restProps
	}: WithoutChildrenOrChild<DialogPrimitive.RootProps> &
		WithoutChildrenOrChild<CommandPrimitive.RootProps> & {
			portalProps?: DialogPrimitive.PortalProps;
			children: Snippet;
			title?: string;
			description?: string;
			showCloseButton?: boolean;
			class?: string;
		} = $props();
</script>

<!-- The header sits *inside* Dialog.Content, which is a change from the
     generated shadcn-svelte file. As generated it was a sibling of the content,
     so the sr-only title and description rendered on every page whether or not
     the palette was open -- verified in the browser: a 1x16px node reading
     "Jump to / Search for an item..." above the browse screen, invisible but
     live in the accessibility tree. It also left the dialog with nothing to
     point aria-labelledby at once it did open. Inside, it both names the dialog
     and exists only while the dialog does. -->
<Dialog.Root bind:open {...restProps}>
	<Dialog.Content
		class={cn("rounded-xl! top-1/3 translate-y-0 overflow-hidden p-0", className)}
		{showCloseButton}
		{portalProps}
	>
		<Dialog.Header class="sr-only">
			<Dialog.Title>{title}</Dialog.Title>
			<Dialog.Description>{description}</Dialog.Description>
		</Dialog.Header>
		<Command {...restProps} bind:value bind:ref {children} />
	</Dialog.Content>
</Dialog.Root>
