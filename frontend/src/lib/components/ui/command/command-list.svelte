<script lang="ts">
	import { Command as CommandPrimitive } from 'bits-ui';
	import { cn } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'default',
		...restProps
	}: CommandPrimitive.ListProps & {
		// 'palette' is the command palette's taller list, inset from the dialog's edges
		// 'popover' is a list under a search field in a popover, which grows until the popover reaches the screen edge and keeps a little room below the field
		variant?: 'default' | 'palette' | 'popover';
	} = $props();
</script>

<CommandPrimitive.List
	bind:ref
	data-slot="command-list"
	class={cn(
		'no-scrollbar max-h-[min(26rem,60dvh)] scroll-py-1 outline-none overflow-x-hidden overflow-y-auto',
		variant === 'palette' && 'max-h-[min(28rem,60dvh)] scroll-py-2 p-2',
		variant === 'popover' &&
			'max-h-[min(26rem,calc(var(--bits-popover-content-available-height)-4rem))] pt-1',
		className
	)}
	{...restProps}
/>
