<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		inset,
		size = 'default',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		inset?: boolean;
		// 'sm' is the quieter caption above a list of choices, such as the workspace switcher's
		size?: 'default' | 'sm';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="dropdown-menu-label"
	data-inset={inset}
	class={cn(
		'text-muted-foreground px-2 py-1.5 text-sm data-[inset]:pl-8',
		size === 'sm' && 'text-xs',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
