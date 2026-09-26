<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> = $props();
</script>

<div
	bind:this={ref}
	data-slot="card-content"
	class={cn(
		'bg-card ring-border flex-1 rounded-lg p-4 shadow-xs ring-1 group-data-[size=sm]/card:p-3',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
