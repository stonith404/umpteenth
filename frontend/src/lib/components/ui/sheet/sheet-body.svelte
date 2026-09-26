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

<!-- The scrolling middle of a sheet, between Sheet.Header and Sheet.Footer, on the same 16px gutter so their edges line up -->
<div
	bind:this={ref}
	data-slot="sheet-body"
	class={cn('flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-4 pb-4', className)}
	{...restProps}
>
	{@render children?.()}
</div>
