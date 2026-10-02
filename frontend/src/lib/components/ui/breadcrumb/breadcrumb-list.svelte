<script lang="ts">
	import { cn, type WithElementRef } from '#lib/utils/style.js';
	import type { HTMLOlAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLOlAttributes> = $props();
</script>

<!-- Like Kumo's breadcrumbs the trail stays on one line: ancestors keep their width and the current page truncates, so it never wraps out of a fixed-height header -->
<!-- The trail is the header's title, so it reads at body size rather than as small print -->
<ol
	bind:this={ref}
	data-slot="breadcrumb-list"
	class={cn(
		'text-muted-foreground flex min-w-0 items-center gap-1.5 overflow-hidden text-base whitespace-nowrap sm:gap-2',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</ol>
