<script lang="ts" module>
	import { type VariantProps, tv } from "tailwind-variants";
	import { cn, type WithElementRef } from "@stockroom/ui/utils";
	import type { HTMLAnchorAttributes, HTMLButtonAttributes } from "svelte/elements";

	/*
	 * Reworked from the generated variants (design-system.md §5.1).
	 *
	 * There is no brand accent colour in this app: `primary` is near-white on
	 * the dark ground, because green/blue/amber/red are reserved for asset state
	 * and a primary button that shared a hue with an "Available" chip would make
	 * the eye stop trusting the hue as a signal (§1.2, §3.2).
	 *
	 * `link` is deleted. A button that looks like a link has no place here.
	 *
	 * `default` and `outline` survive as aliases of `primary` and `secondary`
	 * only because shadcn's own generated components ask for those names
	 * internally (alert-dialog's action/cancel, the calendar's nav buttons).
	 * Application code should use the four names the design doc lists.
	 *
	 * Heights bind to --control-h rather than a fixed `h-8`, so every button
	 * grows with the surrounding density (§4). A hardcoded height is a bug: it
	 * will not scale into kiosk mode.
	 */
	export const buttonVariants = tv({
		base: "focus-visible:ring-ring/50 aria-invalid:border-destructive rounded-lg border border-transparent bg-clip-padding font-medium text-[length:var(--text-body)] focus-visible:ring-3 [&_svg:not([class*='size-'])]:size-4 group/button inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap transition-colors duration-(--dur-fast) ease-(--ease-brand) outline-none select-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
		variants: {
			variant: {
				primary: "bg-primary text-primary-foreground hover:bg-(--primary-hover)",
				default: "bg-primary text-primary-foreground hover:bg-(--primary-hover)",
				secondary: "border-line-control text-fg hover:bg-raised aria-expanded:bg-raised",
				outline: "border-line-control text-fg hover:bg-raised aria-expanded:bg-raised",
				ghost: "text-fg hover:bg-raised aria-expanded:bg-raised",
				destructive:
					"border-destructive/40 text-destructive hover:bg-destructive/15 focus-visible:ring-destructive/40",
			},
			size: {
				default: "h-(--control-h) px-3",
				xs: "h-[calc(var(--control-h)-8px)] gap-1 px-2 text-xs [&_svg:not([class*='size-'])]:size-3",
				sm: "h-[calc(var(--control-h)-4px)] gap-1 px-2.5 [&_svg:not([class*='size-'])]:size-3.5",
				lg: "h-[calc(var(--control-h)+8px)] px-4",
				icon: "size-(--control-h)",
				"icon-xs": "size-[calc(var(--control-h)-8px)] [&_svg:not([class*='size-'])]:size-3",
				"icon-sm": "size-[calc(var(--control-h)-4px)] [&_svg:not([class*='size-'])]:size-3.5",
				"icon-lg": "size-[calc(var(--control-h)+8px)]",
				/* Anything a student touches in a hurry: the WCAG 2.2 target-size
				   minimum with margin, and the floor in kiosk mode (§4). */
				tap: "min-h-(--tap) h-(--tap) px-5 text-[length:var(--text-body)]",
			},
		},
		defaultVariants: {
			variant: "primary",
			size: "default",
		},
	});

	export type ButtonVariant = VariantProps<typeof buttonVariants>["variant"];
	export type ButtonSize = VariantProps<typeof buttonVariants>["size"];

	export type ButtonProps = WithElementRef<HTMLButtonAttributes> &
		WithElementRef<HTMLAnchorAttributes> & {
			variant?: ButtonVariant;
			size?: ButtonSize;
		};
</script>

<script lang="ts">
	let {
		class: className,
		variant = "default",
		size = "default",
		ref = $bindable(null),
		href = undefined,
		type = "button",
		disabled,
		children,
		...restProps
	}: ButtonProps = $props();
</script>

{#if href}
	<a
		bind:this={ref}
		data-slot="button"
		class={cn(buttonVariants({ variant, size }), className)}
		href={disabled ? undefined : href}
		aria-disabled={disabled}
		role={disabled ? "link" : undefined}
		tabindex={disabled ? -1 : undefined}
		{...restProps}
	>
		{@render children?.()}
	</a>
{:else}
	<button
		bind:this={ref}
		data-slot="button"
		class={cn(buttonVariants({ variant, size }), className)}
		{type}
		{disabled}
		{...restProps}
	>
		{@render children?.()}
	</button>
{/if}
