<script lang="ts" module>
	import { tv, type VariantProps } from 'tailwind-variants';

	// Kumo's nav item: one tint marks hover, focus and the current page, and every item keeps the same weight
	export const sidebarMenuButtonVariants = tv({
		base: [
			'peer/menu-button group/menu-button text-sidebar-foreground relative flex w-full min-w-0 items-center gap-3 overflow-hidden rounded-lg px-3 text-left text-sm font-medium outline-hidden',
			// The hit area spans the 1px gaps between items, so the pointer never falls through between two of them
			'before:absolute before:inset-x-0 before:-inset-y-px',
			'hover:bg-sidebar-accent data-active:bg-sidebar-accent data-open:bg-sidebar-accent focus-visible:bg-sidebar-accent focus-visible:text-sidebar-accent-foreground',
			'transition-[padding,margin,color,box-shadow] duration-(--sidebar-duration) ease-(--sidebar-easing)',
			// Collapsed, the button is the rail's 34px and 9px of padding centers the 16px icon in it
			'group-data-[collapsible=icon]:px-[9px]',
			'group-has-data-[sidebar=menu-action]/menu-item:pr-8',
			'[&_svg]:size-4 [&_svg]:shrink-0 [&>svg]:opacity-40',
			// Labels fade while the narrowing rail clips them, rather than truncating to an ellipsis that changes every frame
			'[&>:not(svg)]:transition-opacity [&>:not(svg)]:duration-(--sidebar-duration) [&>:not(svg)]:ease-(--sidebar-easing) group-data-[collapsible=icon]:[&>:not(svg)]:opacity-0',
			'disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50'
		],
		variants: {
			variant: {
				default: '',
				// A boxed item like Kumo's quick search, whose box fades away when the sidebar collapses to icons
				// It reads as a field rather than a destination, so its label is set in regular weight
				outline:
					'ring-sidebar-border shadow-xs ring font-normal group-data-[collapsible=icon]:shadow-none group-data-[collapsible=icon]:ring-transparent'
			},
			size: {
				default: 'min-h-8.5',
				sm: 'min-h-7 px-2',
				lg: 'min-h-12'
			}
		},
		defaultVariants: {
			variant: 'default',
			size: 'default'
		}
	});

	export type SidebarMenuButtonVariant = VariantProps<typeof sidebarMenuButtonVariants>['variant'];
	export type SidebarMenuButtonSize = VariantProps<typeof sidebarMenuButtonVariants>['size'];
</script>

<script lang="ts">
	import { mergeProps } from 'bits-ui';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { cn, type WithElementRef, type WithoutChildrenOrChild } from '$lib/utils/style.js';
	import { useSidebar } from './context.svelte.js';
	import type { ComponentProps, Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		child,
		variant = 'default',
		size = 'default',
		isActive = false,
		tooltipContent,
		tooltipContentProps,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLButtonElement>, HTMLButtonElement> & {
		isActive?: boolean;
		variant?: SidebarMenuButtonVariant;
		size?: SidebarMenuButtonSize;
		tooltipContent?: Snippet | string;
		tooltipContentProps?: WithoutChildrenOrChild<ComponentProps<typeof Tooltip.Content>>;
		child?: Snippet<[{ props: Record<string, unknown> }]>;
	} = $props();

	const sidebar = useSidebar();

	const buttonProps = $derived({
		class: cn(sidebarMenuButtonVariants({ variant, size }), className),
		'data-slot': 'sidebar-menu-button',
		'data-sidebar': 'menu-button',
		'data-size': size,
		'data-active': isActive,
		...restProps
	});
</script>

{#snippet Button({ props }: { props?: Record<string, unknown> })}
	{@const mergedProps = mergeProps(buttonProps, props)}
	{#if child}
		{@render child({ props: mergedProps })}
	{:else}
		<button bind:this={ref} {...mergedProps}>
			{@render children?.()}
		</button>
	{/if}
{/snippet}

{#if !tooltipContent}
	{@render Button({})}
{:else}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				{@render Button({ props })}
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content
			side="right"
			align="center"
			hidden={sidebar.state !== 'collapsed' || sidebar.isMobile}
			{...tooltipContentProps}
		>
			{#if typeof tooltipContent === 'string'}
				{tooltipContent}
			{:else if tooltipContent}
				{@render tooltipContent()}
			{/if}
		</Tooltip.Content>
	</Tooltip.Root>
{/if}
