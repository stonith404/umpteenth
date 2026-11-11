<script lang="ts" generics="TData">
	import { Button } from '#lib/components/ui/button/index.js';
	import { ButtonGroup } from '#lib/components/ui/button-group/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import { cn } from '#lib/utils/style.js';
	import type { Table } from '@tanstack/table-core';
	import CaretDoubleLeftIcon from 'phosphor-svelte/lib/CaretDoubleLeft';
	import CaretDoubleRightIcon from 'phosphor-svelte/lib/CaretDoubleRight';
	import CaretLeftIcon from 'phosphor-svelte/lib/CaretLeft';
	import CaretRightIcon from 'phosphor-svelte/lib/CaretRight';

	let {
		table,
		pageSizes,
		controls,
		class: className
	}: {
		table: Table<TData>;
		pageSizes: number[];
		// Whether to show the page size and the pager next to the row count
		controls: boolean;
		class?: string;
	} = $props();

	const pagination = $derived(table.getState().pagination);
	const total = $derived(table.getRowCount());
	const firstRow = $derived(total === 0 ? 0 : pagination.pageIndex * pagination.pageSize + 1);
	const lastRow = $derived(Math.min((pagination.pageIndex + 1) * pagination.pageSize, total));

	// A page size from a link that isn't in the list still shows as the selected option
	const sizes = $derived([...new Set([...pageSizes, pagination.pageSize])].sort((a, b) => a - b));
</script>

<!-- Kumo's pagination: the range on the left, the page size and a joined previous/next control on the right -->
<div class={cn('flex items-center justify-between gap-3 text-sm', className)}>
	<p class="text-muted-foreground" aria-live="polite">
		Showing <span class="numeric">{firstRow}–{lastRow}</span> of
		<span class="numeric">{total}</span>
	</p>
	{#if controls}
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
			<!-- Cloudflare's joined pager: first, previous, the current page, next and last, all in one framed strip -->
			<nav aria-label="Pagination" class="flex">
				<ButtonGroup>
					<Button
						variant="outline"
						size="icon"
						aria-label="First page"
						disabled={!table.getCanPreviousPage()}
						onclick={() => table.firstPage()}
					>
						<CaretDoubleLeftIcon />
					</Button>
					<Button
						variant="outline"
						size="icon"
						aria-label="Previous page"
						disabled={!table.getCanPreviousPage()}
						onclick={() => table.previousPage()}
					>
						<CaretLeftIcon />
					</Button>
					<span
						class="bg-card border-border numeric flex h-9 min-w-12 items-center justify-center border px-2 text-base shadow-xs"
						aria-current="page"
					>
						{pagination.pageIndex + 1}
					</span>
					<Button
						variant="outline"
						size="icon"
						aria-label="Next page"
						disabled={!table.getCanNextPage()}
						onclick={() => table.nextPage()}
					>
						<CaretRightIcon />
					</Button>
					<Button
						variant="outline"
						size="icon"
						aria-label="Last page"
						disabled={!table.getCanNextPage()}
						onclick={() => table.lastPage()}
					>
						<CaretDoubleRightIcon />
					</Button>
				</ButtonGroup>
			</nav>
		</div>
	{/if}
</div>
