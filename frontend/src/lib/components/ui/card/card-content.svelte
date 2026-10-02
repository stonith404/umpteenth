<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		padding = 'default',
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		// 'none' lets content that brings its own gutter, like a flush data table, run edge to edge
		// 'tight' keeps the side padding but trims the top and bottom, for a single status line such as a spinner and its message
		padding?: 'default' | 'none' | 'tight';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="card-content"
	class={cn(
		'bg-card ring-border flex-1 rounded-lg p-4 shadow-xs ring-1 group-data-[size=sm]/card:p-3 max-sm:group-data-[size=tile]/card:p-3',
		padding === 'none' && 'p-0',
		padding === 'tight' && 'py-2',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
