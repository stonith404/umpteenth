<script lang="ts">
	import * as Collapsible from '$lib/components/ui/collapsible';
	import { cn } from '$lib/utils/style';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import type { Snippet } from 'svelte';

	let {
		label,
		open = $bindable(false),
		hint,
		children
	}: {
		label: string;
		open?: boolean;
		// Shown next to the label, e.g. the size of the hidden content
		hint?: string;
		children: Snippet;
	} = $props();
</script>

<Collapsible.Root bind:open class="flex flex-col gap-1.5">
	<Collapsible.Trigger
		class="text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-xs font-medium"
	>
		<ChevronRightIcon class={cn('size-3.5 transition-transform', open && 'rotate-90')} />
		{label}
		{#if hint}
			<span class="font-normal opacity-70">{hint}</span>
		{/if}
	</Collapsible.Trigger>
	<Collapsible.Content>
		{@render children()}
	</Collapsible.Content>
</Collapsible.Root>
