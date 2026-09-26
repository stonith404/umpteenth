<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		size = 'default',
		border = 'always',
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLElement>> & {
		// 'fit' grows with its content, such as a workspace switcher taller than the footer's usual row
		size?: 'default' | 'fit';
		// 'collapsed' draws the top border only in the rail, for a footer that otherwise looks empty
		border?: 'always' | 'collapsed';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="sidebar-footer"
	data-sidebar="footer"
	class={cn(
		'flex h-12 shrink-0 items-center gap-4 overflow-hidden border-t px-4 transition-[padding] duration-(--sidebar-duration) ease-(--sidebar-easing)',
		// Collapsed, the gutters match the content's so the trigger lines up with the icons above it
		'group-data-[collapsible=icon]:px-[11px]',
		size === 'fit' && 'h-auto min-h-12 px-3.5 py-2 transition-[padding,border-color]',
		border === 'collapsed' && 'border-transparent group-data-[collapsible=icon]:border-border',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
