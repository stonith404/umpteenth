<script lang="ts">
	import { Tooltip as TooltipPrimitive } from 'bits-ui';
	import { cn } from '$lib/utils/style.js';
	import TooltipPortal from './tooltip-portal.svelte';
	import type { ComponentProps } from 'svelte';
	import type { WithoutChildrenOrChild } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		sideOffset = 0,
		side = 'top',
		children,
		arrowClasses,
		portalProps,
		mono = false,
		...restProps
	}: TooltipPrimitive.ContentProps & {
		arrowClasses?: string;
		portalProps?: WithoutChildrenOrChild<ComponentProps<typeof TooltipPortal>>;
		// Sets the tip in small monospace, for exact values like timestamps, IDs and paths
		mono?: boolean;
	} = $props();
</script>

<TooltipPortal {...portalProps}>
	<TooltipPrimitive.Content
		bind:ref
		data-slot="tooltip-content"
		{sideOffset}
		{side}
		class={cn(
			'data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-[state=delayed-open]:animate-in data-[state=delayed-open]:fade-in-0 data-[state=delayed-open]:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 ring-border text-popover-foreground relative isolate inline-flex w-fit max-w-xs origin-(--bits-tooltip-content-transform-origin) items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm shadow-md ring-1 has-data-[slot=kbd]:pr-1.5 **:data-[slot=kbd]:relative **:data-[slot=kbd]:isolate **:data-[slot=kbd]:z-50 **:data-[slot=kbd]:rounded-lg z-50',
			mono && 'font-mono text-xs',
			className
		)}
		{...restProps}
	>
		<span
			aria-hidden="true"
			class="tooltip-surface pointer-events-none absolute inset-0 -z-1 rounded-[inherit]"
		></span>
		{@render children?.()}
		<TooltipPrimitive.Arrow>
			{#snippet child({ props })}
				<div class="pointer-events-none size-2.5 z-50" {...props}>
					<div
						class={cn(
							'tooltip-surface border-border absolute top-[-29.14px] -left-2.5 size-7.5 rotate-45 rounded-[2px] border-r border-b [clip-path:polygon(100%_calc(100%-10px),100%_100%,calc(100%-10px)_100%)]',
							arrowClasses
						)}
					></div>
				</div>
			{/snippet}
		</TooltipPrimitive.Arrow>
	</TooltipPrimitive.Content>
</TooltipPortal>

<style>
	/* Kumo's tooltips are solid like its menus, the arrow shares the surface so both read as one shape */
	.tooltip-surface {
		background-color: var(--popover);
	}
</style>
