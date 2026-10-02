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
	data-slot="card-footer"
	class={cn(
		// A docked card's footer is the whole bar below xl, so it keeps only a thin frame around its buttons
		'px-4 py-3 flex items-center gap-2 max-xl:group-data-[variant=dock]/card:p-2',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
