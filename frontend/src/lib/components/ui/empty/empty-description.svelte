<script lang="ts">
	import { cn, type WithElementRef } from '$lib/utils/style.js';
	import type { HTMLAttributes } from 'svelte/elements';

	let {
		ref = $bindable(null),
		class: className,
		children,
		size = 'default',
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
		// 'page' sets the description of a message that replaces a whole page at body size, matching Empty's size
		size?: 'default' | 'page';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="empty-description"
	class={cn(
		'text-sm/relaxed text-sm/relaxed text-muted-foreground [&>a]:underline [&>a]:underline-offset-4 [&>a:hover]:text-primary',
		size === 'page' && 'text-base',
		className
	)}
	{...restProps}
>
	{@render children?.()}
</div>
