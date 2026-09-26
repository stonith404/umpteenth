<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLElement>> = $props();
</script>

<div
	bind:this={ref}
	data-slot="sidebar-header"
	data-sidebar="header"
	class={cn('flex h-14.5 shrink-0 items-center gap-1 overflow-hidden border-b px-3', className)}
	{...restProps}
>
	{@render children?.()}
</div>
