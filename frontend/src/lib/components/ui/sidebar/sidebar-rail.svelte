<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import { useSidebar } from './context.svelte.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLButtonElement>, HTMLButtonElement> = $props();

	const sidebar = useSidebar();
</script>

<button
	bind:this={ref}
	data-sidebar="rail"
	data-slot="sidebar-rail"
	aria-label="Toggle sidebar"
	tabindex={-1}
	onclick={sidebar.toggle}
	class={cn(
		// The rail stays inside the sidebar's edge, since Chrome leaves stale frames of the collapse animation in any part of the fixed sidebar layer that hangs over the page
		'hover:after:bg-primary/20 absolute inset-y-0 z-20 hidden w-2 cursor-pointer after:absolute after:inset-y-0 after:w-0.5 after:transition-colors sm:flex',
		'group-data-[side=left]:end-0 group-data-[side=left]:after:end-0 group-data-[side=right]:start-0 group-data-[side=right]:after:start-0',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</button>
