<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'plain',
		size = 'default',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		// 'plain' has no frame, for empty tables and cards, 'panel' is Kumo's filled empty panel for a standalone empty state on the page
		variant?: 'plain' | 'panel';
		// The vertical room around the message: 'sm' inside lists and dialogs, 'md' in a card, 'default' on its own, 'lg' for a page that is empty as a whole
		// 'page' is the message that replaces a whole page inside the app shell, which already provides the side gutters
		size?: 'sm' | 'md' | 'default' | 'lg' | 'page';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="empty"
	data-variant={variant}
	class={cn(
		'flex w-full min-w-0 flex-1 flex-col items-center justify-center gap-4 rounded-lg p-12 text-center text-balance',
		variant === 'panel' && 'border-fill bg-card border',
		size === 'sm' && 'py-6',
		size === 'md' && 'py-10',
		size === 'lg' && 'py-16',
		size === 'page' && 'px-0 py-16 sm:py-24',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
