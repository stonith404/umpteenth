<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> = $props();
</script>

<!-- Joins adjacent buttons into one segmented control: inner corners square off and neighbours share a border -->
<div
	bind:this={ref}
	role="group"
	data-slot="button-group"
	class={cn(
		'flex [&>*:not(:first-child)]:-ml-px [&>*:not(:first-child)]:rounded-l-none [&>*:not(:last-child)]:rounded-r-none',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
