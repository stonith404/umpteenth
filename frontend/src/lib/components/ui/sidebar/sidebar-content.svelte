<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
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
	data-slot="sidebar-content"
	data-sidebar="content"
	class={cn(
		'no-scrollbar flex min-h-0 flex-1 flex-col overflow-x-hidden overflow-y-auto px-3.5 py-3 transition-[padding] duration-(--sidebar-duration) ease-(--sidebar-easing)',
		// Collapsed, the narrower gutters leave exactly one 34px button between them
		'group-data-[collapsible=icon]:px-[11px]',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
