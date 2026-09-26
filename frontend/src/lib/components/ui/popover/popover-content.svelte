<script lang="ts">
	import { Popover as PopoverPrimitive } from 'bits-ui';
	import PopoverPortal from './popover-portal.svelte';
	import { cn, type WithoutChildrenOrChild } from '$lib/utils/style.js';
	import type { ComponentProps } from 'svelte';

	let {
		ref = $bindable(null),
		class: className,
		sideOffset = 4,
		align = 'center',
		// A menu pushed back into view near the screen edge keeps the same small gap as select lists
		collisionPadding = 8,
		sameWidth = false,
		fitScreen = false,
		padding = 'default',
		portalProps,
		...restProps
	}: PopoverPrimitive.ContentProps & {
		portalProps?: WithoutChildrenOrChild<ComponentProps<typeof PopoverPortal>>;
		sameWidth?: boolean;
		// Never wider than the screen minus the page gutters, for a popover that can be wider than a phone
		fitScreen?: boolean;
		// 'none' drops the padding for content that brings its own, like a command list or a calendar
		padding?: 'default' | 'none';
	} = $props();
</script>

<PopoverPortal {...portalProps}>
	<PopoverPrimitive.Content
		bind:ref
		data-slot="popover-content"
		{sideOffset}
		{align}
		{collisionPadding}
		class={cn(
			'bg-popover text-popover-foreground data-open:animate-in data-closed:animate-out data-closed:fade-out-0 data-open:fade-in-0 data-closed:zoom-out-95 data-open:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 ring-border flex flex-col gap-4 rounded-lg p-4 text-sm shadow-md ring-1 duration-100 data-[side=inline-start]:slide-in-from-right-2 data-[side=inline-end]:slide-in-from-left-2 z-50 w-72 origin-(--transform-origin) outline-hidden',
			sameWidth && 'w-(--bits-popover-anchor-width)',
			fitScreen && 'max-w-[calc(100vw-2rem)]',
			padding === 'none' && 'p-0',
			className
		)}
		{...restProps}
	/>
</PopoverPortal>
