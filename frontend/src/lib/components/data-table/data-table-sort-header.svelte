<script lang="ts" generics="TData">
	import { Button } from '$lib/components/ui/button';
	import type { Column } from '@tanstack/table-core';
	import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
	import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
	import ChevronsUpDownIcon from '@lucide/svelte/icons/chevrons-up-down';

	let { column, label }: { column: Column<TData, unknown>; label: string } = $props();

	const sorted = $derived(column.getIsSorted());
</script>

<!-- Shift-click adds the column as a secondary sort, the server accepts several sort keys -->
<Button
	variant="ghost"
	size="sm"
	class="text-foreground -ml-2.5 h-8 text-sm font-semibold"
	data-sorted={!!sorted}
	title="Sort by {label.toLowerCase()} (shift-click to add a secondary sort)"
	onclick={(event: MouseEvent) => column.getToggleSortingHandler()?.(event)}
>
	{label}
	{#if sorted === 'asc'}
		<ArrowUpIcon data-icon="inline-end" />
	{:else if sorted === 'desc'}
		<ArrowDownIcon data-icon="inline-end" />
	{:else}
		<ChevronsUpDownIcon data-icon="inline-end" class="opacity-40" />
	{/if}
</Button>
