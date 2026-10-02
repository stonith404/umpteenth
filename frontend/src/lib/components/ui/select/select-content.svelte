<script lang="ts">
	import { Select as SelectPrimitive } from 'bits-ui';
	import SelectPortal from './select-portal.svelte';
	import SelectScrollUpButton from './select-scroll-up-button.svelte';
	import SelectScrollDownButton from './select-scroll-down-button.svelte';
	import { cn, type WithoutChild } from '#lib/utils/style.js';
	import type { ComponentProps } from 'svelte';
	import type { WithoutChildrenOrChild } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		sideOffset = 4,
		// Kumo opens the list from the trigger's start edge, so options wider than a narrow trigger grow to one side instead of both
		align = 'start',
		// A list pushed back into view by a trigger near the screen edge keeps a small gap to it instead of touching it
		collisionPadding = 8,
		portalProps,
		children,
		preventScroll = true,
		...restProps
	}: WithoutChild<SelectPrimitive.ContentProps> & {
		portalProps?: WithoutChildrenOrChild<ComponentProps<typeof SelectPortal>>;
	} = $props();
</script>

<SelectPortal {...portalProps}>
	<SelectPrimitive.Content
		bind:ref
		{sideOffset}
		{align}
		{collisionPadding}
		{preventScroll}
		data-slot="select-content"
		class={cn(
			'bg-popover text-popover-foreground data-open:animate-in data-closed:animate-out data-closed:fade-out-0 data-open:fade-in-0 data-closed:zoom-out-95 data-open:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=inline-end]:slide-in-from-left-2 ring-border relative isolate z-50 overflow-x-hidden overflow-y-auto rounded-lg p-1.5 shadow-lg ring-1 duration-100',
			// Like Kumo's list it is at least as wide as its trigger, so their edges line up, and never wider than the screen, so long options wrap instead of running off it
			'min-w-[max(9rem,var(--bits-select-anchor-width))] sm:max-w-(--bits-select-content-available-width)',
			// On phones a list under a full-width trigger also keeps to the trigger's width rather than hanging past its edges, while a short trigger still gets a list wide enough to read
			'max-sm:max-w-[min(var(--bits-select-content-available-width),max(var(--bits-select-anchor-width),16rem))]',
			className
		)}
		{...restProps}
	>
		<SelectScrollUpButton />
		<SelectPrimitive.Viewport class="h-(--bits-select-anchor-height) w-full scroll-my-1">
			{@render children?.()}
		</SelectPrimitive.Viewport>
		<SelectScrollDownButton />
	</SelectPrimitive.Content>
</SelectPortal>
