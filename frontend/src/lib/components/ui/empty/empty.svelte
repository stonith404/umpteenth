<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'plain',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		// 'plain' has no frame, for empty tables and cards, 'panel' is Kumo's filled empty panel for a standalone empty state on the page
		variant?: 'plain' | 'panel';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="empty"
	data-variant={variant}
	class={cn(
		'flex w-full min-w-0 flex-1 flex-col items-center justify-center gap-4 rounded-lg p-12 text-center text-balance',
		variant === 'panel' && 'border-fill bg-card border',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
