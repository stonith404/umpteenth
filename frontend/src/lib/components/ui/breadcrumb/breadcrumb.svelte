<script lang="ts">
	import type { WithElementRef } from '#lib/utils/style.js';
	import { cn } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		concealed = false,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLElement>> & {
		// Fades the trail out and lets it settle a few pixels lower, as the dashboard's header does while the page's own title is in view
		// It stays in the accessibility tree, so only use it on a trail without links
		concealed?: boolean;
	} = $props();
</script>

<nav
	bind:this={ref}
	data-slot="breadcrumb"
	aria-label="breadcrumb"
	class={cn(
		'transition-[opacity,translate] duration-250 motion-reduce:transition-none',
		concealed && 'translate-y-1 opacity-0',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</nav>
