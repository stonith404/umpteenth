<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLThAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLThAttributes> = $props();
</script>

<th
	bind:this={ref}
	data-slot="table-head"
	scope="col"
	class={cn(
		// Every table shares one header style, so plain tables in cards and data tables read alike
		'text-foreground h-10 px-3 text-left align-middle text-sm font-semibold whitespace-nowrap [&:has([role=checkbox])]:pr-0',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</th>
