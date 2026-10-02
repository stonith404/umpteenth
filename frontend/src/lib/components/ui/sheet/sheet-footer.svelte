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
	data-slot="sheet-footer"
	class={cn(
		// Pinned under the scrolling body with a hairline like Kumo's layer footer, on the same 16px gutter as the header
		'border-hairline mt-auto flex flex-col gap-2 border-t p-4',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
