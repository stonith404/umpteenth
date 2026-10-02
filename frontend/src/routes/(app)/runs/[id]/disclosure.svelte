<script lang="ts">
	import * as Collapsible from '#lib/components/ui/collapsible/index.js';
	import { cn } from '#lib/utils/style.js';
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

<Collapsible.Root bind:open class="flex flex-col">
	<Collapsible.Trigger variant="quiet" class="flex w-fit items-center">
		<ChevronRightIcon class={cn('size-3.5 transition-transform', open && 'rotate-90')} />
		{label}
		{#if hint}
			<span class="font-normal opacity-70">{hint}</span>
		{/if}
	</Collapsible.Trigger>
	<Collapsible.Content class="mt-1.5">
		{@render children()}
	</Collapsible.Content>
</Collapsible.Root>
