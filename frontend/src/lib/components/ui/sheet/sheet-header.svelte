<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
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
	data-slot="sheet-header"
	class={cn(
		// Header, body and footer share one 16px gutter, and the right padding keeps long titles clear of the close button
		'flex flex-col gap-1.5 p-4 pr-12',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
