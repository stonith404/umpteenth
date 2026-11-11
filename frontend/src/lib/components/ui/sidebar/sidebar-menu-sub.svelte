<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLUListElement>> = $props();
</script>

<ul
	bind:this={ref}
	data-slot="sidebar-menu-sub"
	data-sidebar="menu-sub"
	class={cn(
		// Cloudflare's nested pages: indented under the parent's icon and hanging off a 1px guide line
		'relative flex min-w-0 flex-col gap-y-px pt-px pl-7 group-data-[collapsible=icon]:hidden',
		'before:bg-sidebar-border before:pointer-events-none before:absolute before:inset-y-px before:left-[19px] before:w-px',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</ul>
