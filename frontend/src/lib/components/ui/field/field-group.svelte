<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		columns,
		variant = 'default',
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		// Lays the fields out side by side from the md breakpoint, in a single column below it, like the cards of the settings pages
		columns?: 2 | 3;
		// 'choices' spaces a list of checkboxes, radios or switches as closely as a checkbox group, so the options read as one list
		variant?: 'default' | 'choices';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="field-group"
	class={cn(
		'gap-7 data-[slot=checkbox-group]:gap-3 *:data-[slot=field-group]:gap-4 group/field-group @container/field-group flex w-full flex-col',
		columns && 'grid grid-cols-1 gap-x-6',
		columns === 2 && 'md:grid-cols-2',
		columns === 3 && 'md:grid-cols-3',
		variant === 'choices' && 'gap-3',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
