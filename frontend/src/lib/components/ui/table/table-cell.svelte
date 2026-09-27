<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLTdAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLTdAttributes> = $props();
</script>

<td
	bind:this={ref}
	data-slot="table-cell"
	class={cn(
		// Inline children such as badges and switches centre on the row too, instead of sitting on the text baseline
		'p-3 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>*]:align-middle',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</td>
