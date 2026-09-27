<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> = $props();
</script>

<div
	bind:this={ref}
	data-slot="card-header"
	class={cn(
		// Small cards shrink the header to the content's gutter, so a tile's label and value start at the same edge
		'gap-0.5 px-4 py-3 group-data-[size=sm]/card:px-3 group-data-[size=sm]/card:py-2.5 group-data-[size=tile]/card:py-2.5 max-sm:group-data-[size=tile]/card:px-3 group/card-header @container/card-header grid auto-rows-min items-start has-data-[slot=card-action]:grid-cols-[1fr_auto] has-data-[slot=card-description]:grid-rows-[auto_auto]',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
