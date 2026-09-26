<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLTableRowElement>> = $props();
</script>

<tr
	bind:this={ref}
	data-slot="table-row"
	class={cn(
		// No hover by default: a highlight promises the row opens something, so only clickable rows add it
		'data-[state=selected]:bg-muted border-hairline border-b transition-colors',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</tr>
