<script lang="ts">
	import { Command as CommandPrimitive, useId } from 'bits-ui';
	import { cn } from '$lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		children,
		heading,
		value,
		variant = 'default',
		...restProps
	}: CommandPrimitive.GroupProps & {
		heading?: string;
		// 'palette' lines its items up with the command palette's list, which carries the side padding itself
		variant?: 'default' | 'palette';
	} = $props();
</script>

<CommandPrimitive.Group
	bind:ref
	data-slot="command-group"
	class={cn(
		'text-foreground **:[[cmdk-group-heading]]:text-muted-foreground overflow-hidden py-2 px-1 **:[[cmdk-group-heading]]:px-3 **:[[cmdk-group-heading]]:py-2 **:[[cmdk-group-heading]]:text-xs **:[[cmdk-group-heading]]:font-medium',
		variant === 'palette' && 'px-0 py-1',
		className
	)}
	value={value ?? heading ?? `----${useId()}`}
	{...restProps}
>
	{#if heading}
		<CommandPrimitive.GroupHeading class="text-muted-foreground px-2 py-1.5 text-xs font-medium">
			{heading}
		</CommandPrimitive.GroupHeading>
	{/if}
	<CommandPrimitive.GroupItems {children} />
</CommandPrimitive.Group>
