<script lang="ts">
	import { Tabs as TabsPrimitive } from 'bits-ui';
	import { cn } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: TabsPrimitive.TriggerProps = $props();
</script>

<TabsPrimitive.Trigger
	bind:ref
	data-slot="tabs-trigger"
	class={cn(
		"gap-2 rounded-md border border-transparent! px-2.5 py-1 text-base font-medium group-data-vertical/tabs:rounded-md group-data-vertical/tabs:px-3 group-data-vertical/tabs:py-1.5 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2 [&_svg:not([class*='size-'])]:size-4 focus-visible:ring-ring text-muted-foreground hover:text-foreground relative inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center whitespace-nowrap transition-all outline-none group-data-[orientation=vertical]/tabs:w-full group-data-[orientation=vertical]/tabs:justify-start focus-visible:ring-2 focus-visible:ring-inset disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
		// Underline tabs are plain text that tints on hover, the list's indicator marks the active one
		'group-data-[variant=line]/tabs-list:h-7 group-data-[variant=line]/tabs-list:flex-none group-data-[variant=line]/tabs-list:rounded group-data-[variant=line]/tabs-list:px-2 group-data-[variant=line]/tabs-list:py-0 group-data-[variant=line]/tabs-list:hover:bg-accent group-data-[variant=line]/tabs-list:data-active:bg-transparent group-data-[variant=line]/tabs-list:data-active:shadow-none group-data-[variant=line]/tabs-list:data-active:ring-0 group-data-[variant=line]/tabs-list:data-active:hover:bg-accent',
		// The active segment is raised like a Kumo control: white, hairline edge, small shadow
		'data-active:bg-card data-active:shadow-xs data-active:ring-1 data-active:ring-border data-active:text-foreground dark:data-active:text-foreground',
		className
	)}
	{...restProps}
/>
