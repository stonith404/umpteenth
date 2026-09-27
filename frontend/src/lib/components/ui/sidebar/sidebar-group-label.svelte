<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		children,
		class: className,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLElement>> = $props();
</script>

<!-- Collapsed, the label folds into a divider between the icon groups, animating its row height so the items below glide up instead of jumping -->
<div
	bind:this={ref}
	data-slot="sidebar-group-label"
	data-sidebar="group-label"
	class={cn(
		'grid grid-rows-[1fr] overflow-hidden border-b border-transparent transition-[grid-template-rows,margin,border-color] duration-(--sidebar-duration) ease-(--sidebar-easing)',
		'group-data-[collapsible=icon]:border-sidebar-border group-data-[collapsible=icon]:my-3 group-data-[collapsible=icon]:grid-rows-[0fr]',
		// The first group has nothing above it to divide from
		'[[data-sidebar=group]:first-child_&]:my-0 [[data-sidebar=group]:first-child_&]:border-transparent'
	)}
	{...restProps}
>
	<div class="min-h-0 min-w-0">
		<div
			class={cn(
				'text-muted-foreground mt-4 mb-2 px-3 text-sm font-medium transition-opacity duration-(--sidebar-duration) ease-(--sidebar-easing) group-data-[collapsible=icon]:opacity-0',
				'[[data-sidebar=group]:first-child_&]:mt-2',
				className
			)}
		>
			{@render children?.()}
		</div>
	</div>
</div>
