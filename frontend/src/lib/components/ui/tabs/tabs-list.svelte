<script lang="ts">
	import { Tabs as TabsPrimitive } from 'bits-ui';
	import { cn } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: TabsPrimitive.ListProps = $props();
</script>

<!-- Kumo's segmented control: a recessed track with a hairline edge, for switching a view in place -->
<TabsPrimitive.List
	bind:ref
	data-slot="tabs-list"
	class={cn(
		'bg-recessed ring-hairline/70 group/tabs-list text-muted-foreground inline-flex w-fit items-center justify-center rounded-lg p-0.5 ring-1 group-data-horizontal/tabs:h-9 group-data-vertical/tabs:rounded-lg group-data-[orientation=vertical]/tabs:h-fit group-data-[orientation=vertical]/tabs:flex-col',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</TabsPrimitive.List>
