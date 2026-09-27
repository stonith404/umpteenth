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
		// 'page' is the larger title of a message that replaces a whole page, matching Empty's size
		size?: 'default' | 'page';
	} = $props();
</script>

<div
	bind:this={ref}
	data-slot="empty-title"
	class={cn('text-lg font-semibold', size === 'page' && 'text-xl', className)}
	{...restProps}
>
	{@render children?.()}
</div>
