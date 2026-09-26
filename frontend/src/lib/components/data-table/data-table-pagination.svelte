<script lang="ts" generics="TData">
	import { Button } from '$lib/components/ui/button';
	import * as Select from '$lib/components/ui/select';
	import type { Table } from '@tanstack/table-core';
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ChevronsLeftIcon from '@lucide/svelte/icons/chevrons-left';
	import ChevronsRightIcon from '@lucide/svelte/icons/chevrons-right';

	let { table, pageSizes }: { table: Table<TData>; pageSizes: number[] } = $props();

	const pagination = $derived(table.getState().pagination);
	const total = $derived(table.getRowCount());
	const pageCount = $derived(Math.max(table.getPageCount(), 1));
	const firstRow = $derived(total === 0 ? 0 : pagination.pageIndex * pagination.pageSize + 1);
	const lastRow = $derived(Math.min((pagination.pageIndex + 1) * pagination.pageSize, total));
</script>

<div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 px-1 text-sm">
	<p class="text-muted-foreground" aria-live="polite">
		{#if total === 0}
			No results
		{:else}
			<span class="numeric">{firstRow}–{lastRow}</span> of <span class="numeric">{total}</span>
		{/if}
	</p>
	<div class="flex flex-wrap items-center gap-x-6 gap-y-3">
		<div class="flex items-center gap-2">
			<span class="text-muted-foreground">Rows per page</span>
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
						{#each pageSizes as size (size)}
							<Select.Item value={String(size)} label={String(size)}>{size}</Select.Item>
						{/each}
					</Select.Group>
				</Select.Content>
			</Select.Root>
		</div>
		<span class="text-muted-foreground">
			Page <span class="numeric text-foreground">{pagination.pageIndex + 1}</span> of
			<span class="numeric text-foreground">{pageCount}</span>
		</span>
		<div class="flex items-center gap-1">
			<Button
				variant="outline"
				size="icon-sm"
				class="hidden sm:inline-flex"
				aria-label="First page"
				disabled={!table.getCanPreviousPage()}
				onclick={() => table.setPageIndex(0)}
			>
				<ChevronsLeftIcon />
			</Button>
			<Button
				variant="outline"
				size="icon-sm"
				aria-label="Previous page"
				disabled={!table.getCanPreviousPage()}
				onclick={() => table.previousPage()}
			>
				<ChevronLeftIcon />
			</Button>
			<Button
				variant="outline"
				size="icon-sm"
				aria-label="Next page"
				disabled={!table.getCanNextPage()}
				onclick={() => table.nextPage()}
			>
				<ChevronRightIcon />
			</Button>
			<Button
				variant="outline"
				size="icon-sm"
				class="hidden sm:inline-flex"
				aria-label="Last page"
				disabled={!table.getCanNextPage()}
				onclick={() => table.setPageIndex(table.getPageCount() - 1)}
			>
				<ChevronsRightIcon />
			</Button>
		</div>
	</div>
</div>
