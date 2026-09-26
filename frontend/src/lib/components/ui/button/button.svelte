<script lang="ts" module>
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAnchorAttributes, HTMLButtonAttributes } from 'svelte/elements';
	import { type VariantProps, tv } from 'tailwind-variants';

	// Kumo's emphasis treatment, shared by the primary and destructive variants through the --emphasis color
	const emphasis =
		'bg-(--emphasis) bg-linear-to-b from-[color-mix(in_oklch,var(--emphasis),white_15%)] to-(--emphasis) text-white border-[color-mix(in_oklch,var(--emphasis),black_10%)] shadow-[inset_0_1px_0_0_color-mix(in_oklch,var(--emphasis),white_30%),0_1px_2px_0_rgb(0_0_0/0.05)] hover:from-[color-mix(in_oklch,var(--emphasis),white_30%)]';

	export const buttonVariants = tv({
		base: "focus-visible:ring-ring aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive dark:aria-invalid:border-destructive/50 rounded-lg border border-transparent bg-clip-padding text-base font-medium focus-visible:ring-2 aria-invalid:ring-3 [&_svg:not([class*='size-'])]:size-4 group/button inline-flex shrink-0 items-center justify-center whitespace-nowrap transition-colors outline-none select-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
		variants: {
			variant: {
				// Kumo's primary: a top-lit gradient of the brand blue with an inner highlight and a darker edge
				default: `[--emphasis:var(--primary)] ${emphasis}`,
				// Kumo's secondary: a white control with a hairline edge
				outline:
					'border-border bg-card text-foreground shadow-xs hover:bg-accent aria-expanded:bg-accent dark:hover:bg-accent',
				secondary:
					'bg-secondary text-secondary-foreground hover:bg-accent aria-expanded:bg-secondary aria-expanded:text-secondary-foreground',
				ghost:
					'hover:bg-accent hover:text-foreground aria-expanded:bg-accent aria-expanded:text-foreground',
				// The dark theme's destructive is a light red meant for text, white labels need Kumo's deeper red-600 behind them
				destructive: `[--emphasis:var(--destructive)] dark:[--emphasis:oklch(0.577_0.245_27.325)] ${emphasis} focus-visible:ring-destructive/40`,
				link: 'text-primary underline-offset-4 hover:underline'
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
			variant?: ButtonVariant;
			size?: ButtonSize;
			isLoading?: boolean;
			autofocus?: boolean;
		};
</script>

<script lang="ts">
	import { Spinner } from '$lib/components/ui/spinner';
	import { onMount } from 'svelte';

	let {
		class: className,
		variant = 'default',
		size = 'default',
		ref = $bindable(null),
		href = undefined,
		type = 'button',
		disabled,
		isLoading = false,
		autofocus = false,
		onclick,
		usePromiseLoading = false,
		children,
		...restProps
	}: ButtonProps & {
		usePromiseLoading?: boolean;
	} = $props();

	onMount(async () => {
		// Using autofocus can be bad for a11y, but in the case of Pocket ID is only used responsibly in places where there are not many choices, and on buttons only where there's descriptive text
		if (autofocus) {
			// Use setTimeout to make sure the element is showing
			setTimeout(() => ref?.focus(), 100);
		}
	});

	async function handleOnClick(event: any) {
		if (usePromiseLoading && onclick) {
			isLoading = true;
			try {
				await onclick(event);
			} finally {
				isLoading = false;
			}
		} else {
			onclick?.(event);
		}
	}
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
		onclick={handleOnClick}
		{...restProps}
	>
		<!-- Children are direct flex items, so the variant's gap spaces icons and text -->
		{#if isLoading}
			<Spinner data-icon="inline-start" />
		{/if}
		{@render children?.()}
	</button>
{/if}
