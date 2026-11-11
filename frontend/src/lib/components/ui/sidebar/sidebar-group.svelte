<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		detached = false,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLElement>> & {
		// Sets the group apart from the next with a gap that closes in the rail, so the rail's icons stay evenly spaced
		detached?: boolean;
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="sidebar-group"
	data-sidebar="group"
	class={cn(
		'relative flex w-full min-w-0 flex-col gap-y-px',
		detached &&
			'mb-3 transition-[margin] duration-(--sidebar-duration) ease-(--sidebar-easing) group-data-[collapsible=icon]:mb-px',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
