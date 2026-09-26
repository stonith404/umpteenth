<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'default',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLParagraphElement>> & {
		// 'label' names the figure of a tile on one line, a step larger from the sm breakpoint so it reads as the tile's heading
		variant?: 'default' | 'label';
	} = $props();
</script>

<p
	bind:this={ref}
	data-slot="card-description"
	class={cn(
		'text-muted-foreground text-sm leading-snug',
		// Restating the size drops leading-snug, so the label keeps the type scale's own line height that the tile's header height is built on
		variant === 'label' && 'truncate text-sm sm:text-base',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</p>
