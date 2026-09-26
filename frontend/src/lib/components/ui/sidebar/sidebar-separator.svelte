<script lang="ts">
	import { Separator } from '$lib/components/ui/separator/index.js';
	import { cn } from '$lib/utils/style.js';
	import type { ComponentProps } from 'svelte';

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: ComponentProps<typeof Separator> = $props();
</script>

<Separator
	bind:ref
	data-slot="sidebar-separator"
	data-sidebar="separator"
	class={cn(
		'bg-sidebar-border mx-2 my-3 transition-[margin] duration-(--sidebar-duration) ease-(--sidebar-easing) data-[orientation=horizontal]:w-auto',
		// Collapsed, it spans the whole button like the dividers the group labels fold into
		'group-data-[collapsible=icon]:mx-0',
		className
	)}
	{...restProps}
/>
