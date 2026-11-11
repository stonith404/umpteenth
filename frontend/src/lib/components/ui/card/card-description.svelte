<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'default',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLParagraphElement>> & {
		// 'label' names the figure of a tile on one line, small and medium like the labels of Cloudflare's chart cards
		variant?: 'default' | 'label';
	} = $props();
</script>

<p
	bind:this={ref}
	data-slot="card-description"
	class={cn(
		'text-muted-foreground text-sm leading-snug',
		// Restating the size drops leading-snug, so the label keeps the type scale's own line height
		variant === 'label' && 'truncate text-xs font-medium',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</p>
