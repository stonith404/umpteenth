<script lang="ts">
	import type { HTMLTableAttributes } from 'svelte/elements';
	import { cn, type WithElementRef } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		flush = false,
		...restProps
	}: WithElementRef<HTMLTableAttributes> & {
		// Insets the outer cells by the --table-gutter of the card the table sits flush in, so its text lines up with the card's padding
		flush?: boolean;
	} = $props();
</script>

<div data-slot="table-container" class="relative w-full overflow-x-auto">
	<table
		bind:this={ref}
		data-slot="table"
		class={cn(
			'w-full caption-bottom text-base',
			flush && '[&_tr>:first-child]:pl-(--table-gutter) [&_tr>:last-child]:pr-(--table-gutter)',
			className
		)}
		{...restProps}
	>
		{@render children?.()}
	</table>
</div>
