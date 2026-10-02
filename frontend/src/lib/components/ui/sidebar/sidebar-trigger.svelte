<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import { useSidebar } from './context.svelte.js';
	import type { HTMLButtonAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		onclick,
		visibleIn = 'always',
		...restProps
	}: WithElementRef<HTMLButtonAttributes, HTMLButtonElement> & {
		// Shows the toggle only while the sidebar is expanded or only in the collapsed rail, fading it with the sidebar's motion so two toggles can crossfade in place
		visibleIn?: 'always' | 'expanded' | 'collapsed';
	} = $props();

	const sidebar = useSidebar();

	const open = $derived(sidebar.isMobile ? sidebar.openMobile : sidebar.open);
</script>

<button
	bind:this={ref}
	type="button"
	data-sidebar="trigger"
	data-slot="sidebar-trigger"
	aria-expanded={open}
	aria-label={open ? 'Collapse sidebar' : 'Expand sidebar'}
	class={cn(
		'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground focus-visible:ring-ring flex size-8.5 shrink-0 items-center justify-center rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-inset',
		visibleIn !== 'always' &&
			'transition-[opacity,visibility] duration-(--sidebar-duration) ease-(--sidebar-easing)',
		visibleIn === 'expanded' &&
			'group-data-[collapsible=icon]:invisible group-data-[collapsible=icon]:opacity-0',
		visibleIn === 'collapsed' &&
			'invisible opacity-0 group-data-[collapsible=icon]:visible group-data-[collapsible=icon]:opacity-100',
		className
	)}
	onclick={(e) => {
		onclick?.(e);
		sidebar.toggle();
	}}
	{...restProps}
>
	<!-- Kumo's panel icon, whose divider slides along with the sidebar edge -->
	<svg
		width="18"
		height="18"
		viewBox="0 0 24 24"
		fill="none"
		stroke="currentColor"
		stroke-width="1.5"
		stroke-linecap="round"
		aria-hidden="true"
		class="shrink-0"
	>
		<path
			d="M21.25 6.72v10.56a2.97 2.97 0 0 1-2.97 2.97H5.72a2.97 2.97 0 0 1-2.97-2.97V6.72a2.97 2.97 0 0 1 2.97-2.97h12.56a2.97 2.97 0 0 1 2.97 2.97"
		/>
		<path
			d="M6.25 7.25v9.5"
			class={cn(
				'transition-transform duration-(--sidebar-duration) ease-(--sidebar-easing)',
				open ? 'translate-x-px' : 'translate-x-[10.5px]'
			)}
		/>
	</svg>
</button>
