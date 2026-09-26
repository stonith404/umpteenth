<script lang="ts">
	import { Command as CommandPrimitive } from 'bits-ui';
	import { cn } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		variant = 'default',
		...restProps
	}: CommandPrimitive.EmptyProps & {
		// 'palette' is the command palette's quieter, roomier message
		variant?: 'default' | 'palette';
	} = $props();
</script>

<CommandPrimitive.Empty
	bind:ref
	data-slot="command-empty"
	class={cn(
		'py-6 text-center text-sm',
		variant === 'palette' && 'text-muted-foreground py-8',
		className
	)}
	{...restProps}
/>
