<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		busy,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLTableSectionElement>> & {
		// Dims the rows while newer ones load, after a short delay so quick loads never flicker
		// Passing it at all, even as false, lets the body fade in and out of that state
		busy?: boolean;
	} = $props();
</script>

<tbody
	bind:this={ref}
	data-slot="table-body"
	class={cn(
		'[&_tr:last-child]:border-0',
		busy !== undefined && 'transition-opacity duration-200',
		busy && 'opacity-60 delay-150',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</tbody>
