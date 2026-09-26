<script lang="ts" module>
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAnchorAttributes, HTMLButtonAttributes } from 'svelte/elements';
	import { type VariantProps, tv } from 'tailwind-variants';

	// Kumo's emphasis treatment, shared by the primary and destructive variants through the --emphasis color
	// A ring flush against a filled button reads as a thicker border, so their focus outline sits 2px off the edge instead
	const emphasis =
		'bg-(--emphasis) bg-linear-to-b from-[color-mix(in_oklch,var(--emphasis),white_15%)] to-(--emphasis) text-white border-[color-mix(in_oklch,var(--emphasis),black_10%)] shadow-[inset_0_1px_0_0_color-mix(in_oklch,var(--emphasis),white_30%),0_1px_2px_0_rgb(0_0_0/0.05)] hover:from-[color-mix(in_oklch,var(--emphasis),white_30%)] focus-visible:ring-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-solid';

	export const buttonVariants = tv({
		base: "focus-visible:ring-ring aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive dark:aria-invalid:border-destructive/50 rounded-lg border border-transparent bg-clip-padding text-base font-medium focus-visible:ring-2 aria-invalid:ring-3 [&_svg:not([class*='size-'])]:size-4 group/button inline-flex shrink-0 items-center justify-center whitespace-nowrap transition-colors outline-none select-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
		variants: {
			variant: {
				// Kumo's primary: a top-lit gradient of the brand blue with an inner highlight and a darker edge
				default: `[--emphasis:var(--primary)] ${emphasis} focus-visible:outline-ring`,
				// Kumo's secondary: a white control with a hairline edge
				outline:
					'border-border bg-card text-foreground shadow-xs hover:bg-accent aria-expanded:bg-accent dark:hover:bg-accent',
				secondary:
					'bg-secondary text-secondary-foreground hover:bg-accent aria-expanded:bg-secondary aria-expanded:text-secondary-foreground',
				ghost:
					'hover:bg-accent hover:text-foreground aria-expanded:bg-accent aria-expanded:text-foreground',
				// The dark theme's destructive is a light red meant for text, white labels need Kumo's deeper red-600 behind them
				destructive: `[--emphasis:var(--destructive)] dark:[--emphasis:oklch(0.577_0.245_27.325)] ${emphasis} focus-visible:outline-destructive`,
				// Kumo's secondary-destructive: a white control with a red label whose edge reddens on hover, for danger-zone triggers whose solid red confirm lives in the dialog
				'destructive-outline':
					'border-border bg-card text-destructive shadow-xs hover:border-destructive/30 focus-visible:ring-destructive/40',
				link: 'text-primary link-underline'
			},
			size: {
				default:
					'h-9 gap-1.5 px-3 has-data-[icon=inline-end]:pr-2.5 has-data-[icon=inline-start]:pl-2.5',
				xs: "h-6.5 gap-1 rounded-md px-2 text-xs has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2 [&_svg:not([class*='size-'])]:size-3",
				sm: 'h-8 gap-1 px-2.5 text-sm has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2',
				lg: 'h-10 gap-1.5 px-4 has-data-[icon=inline-end]:pr-3 has-data-[icon=inline-start]:pl-3',
				icon: 'size-9',
				'icon-xs': "size-6.5 rounded-md [&_svg:not([class*='size-'])]:size-3",
				'icon-sm': 'size-8',
				'icon-lg': 'size-10'
			}
		},
		defaultVariants: {
			variant: 'default',
			size: 'default'
		}
	});

	export type ButtonVariant = VariantProps<typeof buttonVariants>['variant'];
	export type ButtonSize = VariantProps<typeof buttonVariants>['size'];

	export type ButtonProps = WithElementRef<HTMLButtonAttributes> &
		WithElementRef<HTMLAnchorAttributes> & {
			// default (brand primary), outline, secondary, ghost, destructive (solid, for confirms), destructive-outline (danger-zone triggers) or link
			variant?: ButtonVariant;
			size?: ButtonSize;
			// Disables the button and shows a spinner before the label
			isLoading?: boolean;
		};
</script>

<script lang="ts">
	import { Spinner } from '$lib/components/ui/spinner';

	let {
		class: className,
		variant = 'default',
		size = 'default',
		ref = $bindable(null),
		href = undefined,
		type = 'button',
		disabled,
		isLoading = false,
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
		role={disabled ? 'link' : undefined}
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
		disabled={disabled || isLoading}
		{...restProps}
	>
		<!-- Children are direct flex items, so the variant's gap spaces icons and text -->
		{#if isLoading}
			<Spinner data-icon="inline-start" />
		{/if}
		{@render children?.()}
	</button>
{/if}
