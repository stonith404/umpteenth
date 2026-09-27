<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		level = 2,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLHeadingElement>> & {
		// The heading level, 2 for cards directly under the page title and 3 for cards inside a titled section
		level?: 1 | 2 | 3 | 4 | 5 | 6;
	} = $props();
</script>

<!-- A real heading, so screen reader users can jump between a page's cards -->
<svelte:element
	this={`h${level}`}
	bind:this={ref}
	data-slot="card-title"
	class={cn('flex flex-row items-center gap-2 text-base leading-snug font-medium', className)}
	{...restProps}
>
	{@render children?.()}
</svelte:element>
