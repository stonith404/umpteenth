<script lang="ts" generics="TData">
	import { Button } from '$lib/components/ui/button';
	import * as Select from '$lib/components/ui/select';
	import { cn } from '$lib/utils/style';
	import type { Table } from '@tanstack/table-core';
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';

	let {
		table,
		pageSizes,
		class: className
	}: { table: Table<TData>; pageSizes: number[]; class?: string } = $props();

	const pagination = $derived(table.getState().pagination);
	const total = $derived(table.getRowCount());
	const firstRow = $derived(total === 0 ? 0 : pagination.pageIndex * pagination.pageSize + 1);
	const lastRow = $derived(Math.min((pagination.pageIndex + 1) * pagination.pageSize, total));

	// A page size from an old link or a table default outside the list still shows as the selected option
	const sizes = $derived([...new Set([...pageSizes, pagination.pageSize])].sort((a, b) => a - b));
</script>

<!-- Kumo's pagination: the range on the left, the page size and a joined previous/next control on the right -->
<div class={cn('flex items-center justify-between gap-3 text-sm', className)}>
	<p class="text-muted-foreground" aria-live="polite">
		Showing <span class="numeric">{firstRow}–{lastRow}</span> of
		<span class="numeric">{total}</span>
	</p>
	<div class="flex items-center gap-4">
		<div class="hidden items-center gap-2 sm:flex">
			<span class="text-muted-foreground">Per page</span>
			<Select.Root
				type="single"
				value={String(pagination.pageSize)}
				onValueChange={(value) => table.setPageSize(Number(value))}
			>
				<Select.Trigger size="sm" class="w-20" aria-label="Rows per page">
					{pagination.pageSize}
				</Select.Trigger>
				<Select.Content>
					<Select.Group>
						{#each sizes as size (size)}
							<Select.Item value={String(size)} label={String(size)}>{size}</Select.Item>
						{/each}
					</Select.Group>
				</Select.Content>
			</Select.Root>
		</div>
		<nav aria-label="Pagination" class="flex">
			<Button
				variant="outline"
				size="icon-sm"
				class="rounded-r-none"
				aria-label="Previous page"
				disabled={!table.getCanPreviousPage()}
				onclick={() => table.previousPage()}
			>
				<ChevronLeftIcon />
			</Button>
			<Button
				variant="outline"
				size="icon-sm"
				class="-ml-px rounded-l-none"
				aria-label="Next page"
				disabled={!table.getCanNextPage()}
				onclick={() => table.nextPage()}
			>
				<ChevronRightIcon />
			</Button>
		</nav>
	</div>
</div>
