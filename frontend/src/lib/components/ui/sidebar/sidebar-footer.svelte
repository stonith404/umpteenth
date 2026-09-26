<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLElement>> = $props();
</script>

<div
	bind:this={ref}
	data-slot="sidebar-footer"
	data-sidebar="footer"
	class={cn(
		'flex h-12 shrink-0 items-center gap-4 overflow-hidden border-t px-4 transition-[padding] duration-(--sidebar-duration) ease-(--sidebar-easing)',
		// Collapsed, the gutters match the content's so the trigger lines up with the icons above it
		'group-data-[collapsible=icon]:px-[11px]',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
