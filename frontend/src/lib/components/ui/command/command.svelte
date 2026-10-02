<script lang="ts">
	import { cn } from '#lib/utils/style.js';
	import { Command as CommandPrimitive } from 'bits-ui';

	export type CommandRootApi = CommandPrimitive.Root;

	let {
		api = $bindable(null),
		ref = $bindable(null),
		value = $bindable(''),
		class: className,
		variant = 'default',
		...restProps
	}: CommandPrimitive.RootProps & {
		api?: CommandRootApi | null;
		// 'palette' fills a dialog edge to edge, for a command palette that is the whole dialog
		variant?: 'default' | 'palette';
	} = $props();
</script>

<CommandPrimitive.Root
	bind:this={api}
	bind:value
	bind:ref
	data-slot="command"
	class={cn(
		'bg-card text-card-foreground rounded-[inherit] p-1 flex size-full flex-col overflow-hidden',
		variant === 'palette' && 'rounded-none bg-transparent p-0',
		className
	)}
	{...restProps}
/>
