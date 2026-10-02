<script lang="ts" module>
	import { type VariantProps, tv } from 'tailwind-variants';

	// Kumo's badge: a rounded-full label in medium weight, so call sites never override radius, weight or size
	export const badgeVariants = tv({
		base: 'group/badge inline-flex h-5 w-fit shrink-0 items-center justify-center gap-1 overflow-hidden rounded-full border border-transparent px-2 py-0.5 text-xs font-medium whitespace-nowrap transition-colors has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 [&>svg]:pointer-events-none [&>svg]:size-3! [a]:hover:ring-1 [a]:hover:ring-current/30',
		variants: {
			variant: {
				// Kumo's primary badge is ink-inverted, so it reads as a label and never as a brand-blue button
				primary: 'bg-foreground text-background',
				// Kept as an alias of primary for older call sites
				default: 'bg-foreground text-background',
				secondary: 'bg-fill text-fill-foreground',
				// Kumo's subtle status badges: a translucent tint with text in the matching hue
				success: 'bg-success-tint text-success-foreground',
				destructive:
					'bg-destructive-tint text-destructive focus-visible:ring-destructive/20 dark:focus-visible:ring-destructive/40',
				warning: 'bg-warning-tint text-warning-foreground',
				info: 'bg-info-tint text-info-foreground',
				outline: 'border-fill bg-card text-foreground',
				// Unused by Kumo, kept so older call sites keep compiling
				ghost: 'hover:bg-muted hover:text-muted-foreground dark:hover:bg-muted/50',
				link: 'text-primary link-underline'
			},
			// Lifts a badge that sits on the edge of another control, like the sign-in page's Last used hint, off its border
			floating: {
				true: 'shadow-xs'
			}
		},
		defaultVariants: {
			variant: 'primary'
		}
	});

	export type BadgeVariant = VariantProps<typeof badgeVariants>['variant'];
</script>

<script lang="ts">
	import type { HTMLAnchorAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		href,
		class: className,
		variant = 'primary',
		floating = false,
		children,
		...restProps
	}: WithElementRef<HTMLAnchorAttributes> & {
		// One of primary, secondary, success, destructive, warning, info or outline (default is an alias of primary)
		variant?: BadgeVariant;
		floating?: boolean;
	} = $props();
</script>

<svelte:element
	this={href ? 'a' : 'span'}
	bind:this={ref}
	data-slot="badge"
	{href}
	class={cn(badgeVariants({ variant, floating }), className)}
	{...restProps}
>
	{@render children?.()}
</svelte:element>
