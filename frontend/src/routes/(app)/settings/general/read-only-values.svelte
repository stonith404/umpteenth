<script lang="ts" module>
	// One setting as someone who can't change it sees it, with a muted fallback for an empty value
	export type ReadOnlyValue = {
		label: string;
		value?: string | null;
		// Shown instead of an empty value, e.g. 'Not set' or 'Same as the agent model'
		empty?: string;
		// For values that are code, such as image names, URLs and secret names
		mono?: boolean;
	};
</script>

<script lang="ts">
	import { cn } from '#lib/utils/style.js';

	let {
		items,
		columns = 2,
		class: className
	}: {
		items: ReadOnlyValue[];
		// Matches the grid of the form the values stand in for, so both views line up the same
		columns?: 2 | 3;
		class?: string;
	} = $props();

	const gridClass = {
		2: 'sm:grid-cols-2',
		3: 'grid-cols-2 md:grid-cols-3'
	};
</script>

<!-- Plain text instead of disabled controls, so saved values don't read as greyed-out placeholders -->
<dl class={cn('grid grid-cols-1 gap-x-6 gap-y-4 text-sm', gridClass[columns], className)}>
	{#each items as item (item.label)}
		<div class="flex min-w-0 flex-col gap-1">
			<dt class="text-muted-foreground">{item.label}</dt>
			<!-- Long values such as URLs and the notify list wrap, so they get the looser leading of wrapped text -->
			{#if item.value}
				<dd class={cn('leading-snug break-words', item.mono && 'font-mono text-xs')}>
					{item.value}
				</dd>
			{:else}
				<dd class="text-muted-foreground leading-snug">{item.empty ?? 'Not set'}</dd>
			{/if}
		</div>
	{/each}
</dl>
