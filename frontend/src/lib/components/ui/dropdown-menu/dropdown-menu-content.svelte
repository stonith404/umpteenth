<script lang="ts">
	import { cn, type WithoutChildrenOrChild } from '$lib/utils/style.js';
	import { DropdownMenu as DropdownMenuPrimitive } from 'bits-ui';
	import type { ComponentProps } from 'svelte';
	import DropdownMenuPortal from './dropdown-menu-portal.svelte';

	let {
		ref = $bindable(null),
		sideOffset = 4,
		align = 'start',
		// A menu pushed back into view near the screen edge keeps the same small gap as select lists
		collisionPadding = 8,
		portalProps,
		class: className,
		long = false,
		...restProps
	}: DropdownMenuPrimitive.ContentProps & {
		portalProps?: WithoutChildrenOrChild<ComponentProps<typeof DropdownMenuPortal>>;
		// Caps a menu that lists an open-ended number of items at 32rem and the room left on screen, so it scrolls instead of running off the edge
		long?: boolean;
	} = $props();
</script>

<DropdownMenuPortal {...portalProps}>
	<DropdownMenuPrimitive.Content
		bind:ref
		data-slot="dropdown-menu-content"
		{sideOffset}
		{align}
		{collisionPadding}
		class={cn(
			'bg-popover text-popover-foreground data-open:animate-in data-closed:animate-out data-closed:fade-out-0 data-open:fade-in-0 data-closed:zoom-out-95 data-open:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=inline-end]:slide-in-from-left-2 ring-border z-50 rounded-lg p-1.5 shadow-lg ring-1 duration-100 outline-none min-w-36 overflow-x-hidden overflow-y-auto data-closed:overflow-hidden',
			long && 'max-h-[min(32rem,var(--bits-dropdown-menu-content-available-height))]',
			className
		)}
		{...restProps}
	/>
</DropdownMenuPortal>
