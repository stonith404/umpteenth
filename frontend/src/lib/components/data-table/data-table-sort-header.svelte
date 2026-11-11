<script lang="ts" generics="TData">
	import { cn } from '#lib/utils/style.js';
	import type { Column } from '@tanstack/table-core';
	import ArrowDownIcon from 'phosphor-svelte/lib/ArrowDown';
	import ArrowUpIcon from 'phosphor-svelte/lib/ArrowUp';
	import CaretUpDownIcon from 'phosphor-svelte/lib/CaretUpDown';

	let {
		column,
		label,
		align = 'left'
	}: { column: Column<TData, unknown>; label: string; align?: 'left' | 'right' } = $props();

	const sorted = $derived(column.getIsSorted());
</script>

{#snippet icon()}
	{#if sorted === 'asc'}
		<ArrowUpIcon class="size-4 shrink-0" />
	{:else if sorted === 'desc'}
		<ArrowDownIcon class="size-4 shrink-0" />
	{:else}
		<!-- The idle hint only shows while the header is hovered or focused, so only the active sort stands out -->
		<CaretUpDownIcon
			class="size-4 shrink-0 opacity-0 transition-opacity group-focus-visible/sort:opacity-40 group-hover/th:opacity-40"
		/>
	{/if}
{/snippet}

<!-- A plain button inherits the header's typography, and shift-click adds the column as a secondary sort because the server accepts several sort keys -->
<button
	type="button"
	class={cn(
		'group/sort hover:bg-accent focus-visible:ring-ring inline-flex h-8 items-center gap-1 rounded-lg px-2 whitespace-nowrap transition-colors outline-none focus-visible:ring-2',
		align === 'right' ? '-mr-2' : '-ml-2'
	)}
	data-sorted={!!sorted}
	title="Sort by {label.toLowerCase()} (shift-click to add a secondary sort)"
	onclick={(event: MouseEvent) => column.getToggleSortingHandler()?.(event)}
>
	{#if align === 'right'}
		{@render icon()}
	{/if}
	{label}
	{#if align !== 'right'}
		{@render icon()}
	{/if}
</button>
