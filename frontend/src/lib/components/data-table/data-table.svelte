<script lang="ts" generics="TData">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button } from '$lib/components/ui/button';
	import * as InputGroup from '$lib/components/ui/input-group';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Table from '$lib/components/ui/table';
	import { debounced } from '$lib/utils/debounce-util';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { cn } from '$lib/utils/style';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import {
		getCoreRowModel,
		type ColumnDef,
		type ColumnFiltersState,
		type Updater
	} from '@tanstack/table-core';
	import SearchIcon from '@lucide/svelte/icons/search';
	import XIcon from '@lucide/svelte/icons/x';
	import { untrack, type Snippet } from 'svelte';
	import { createSvelteTable } from './create-svelte-table.svelte';
	import DataTableFacetedFilter from './data-table-faceted-filter.svelte';
	import DataTablePagination from './data-table-pagination.svelte';
	import DataTableSortHeader from './data-table-sort-header.svelte';
	import FlexRender from './flex-render.svelte';
	import type { TableFilter, TablePage, TableQuery } from './types';
	import {
		formatSort,
		parseSort,
		readTableUrlState,
		writeTableUrlState,
		type TableUrlConfig,
		type TableUrlState
	} from './url-state';

	type Props = {
		// Column definitions; a column is sortable when its `meta.sortKey` names the server sort key
		columns: ColumnDef<TData, any>[];
		// Loads one page from the server, usually a service method such as `apiTokenService.list`
		fetchPage: (query: TableQuery) => Promise<TablePage<TData>>;
		getRowId: (row: TData) => string;
		// The server's default sort in its own syntax (e.g. `-createdAt`), shown as the active sort when the URL has none
		defaultSort?: string;
		defaultPageSize?: number;
		pageSizes?: number[];
		filters?: TableFilter[];
		searchable?: boolean;
		searchPlaceholder?: string;
		// Namespaces the URL parameters, required when a page shows more than one table
		urlPrefix?: string;
		// Accessible name of the table, also used by tests to find it
		label?: string;
		// Makes whole rows open this link on click, next to any links or buttons inside the row
		rowHref?: (row: TData) => string;
		// Extra toolbar content on the right, e.g. a "Create" button
		actions?: Snippet;
		// Shown when the table has no rows at all, as opposed to no rows matching the search or filters
		empty?: Snippet;
		class?: string;
	};

	let {
		columns,
		fetchPage,
		getRowId,
		defaultSort = '',
		defaultPageSize = 25,
		pageSizes = [10, 25, 50, 100],
		filters = [],
		searchable = true,
		searchPlaceholder = 'Search…',
		urlPrefix,
		label,
		rowHref,
		actions,
		empty,
		class: className
	}: Props = $props();

	const SEARCH_DEBOUNCE_MS = 300;
	const SKELETON_ROWS = 5;

	// Rows are replaced wholesale on every fetch, so they don't need deep reactivity
	let rows = $state.raw<TData[]>([]);
	let total = $state(0);
	let loading = $state(false);
	let loaded = $state(false);
	let loadError = $state<unknown>(null);
	let requestSeq = 0;

	// Map between TanStack column IDs and server sort keys in both directions
	const sortKeys = $derived.by(() => {
		const pairs = columns.flatMap((column) => {
			const id = columnId(column);
			const key = column.meta?.sortKey;
			return id && key ? [[id, key] as const] : [];
		});
		return {
			byColumnId: new Map(pairs),
			byKey: new Map(pairs.map(([id, key]) => [key, id]))
		};
	});

	// Only columns that declare a server sort key can be sorted
	const tableColumns = $derived(
		columns.map((column) => ({ ...column, enableSorting: !!column.meta?.sortKey }))
	);

	// The URL is the single source of truth, so links, reloads and back/forward all restore the same view
	const urlConfig = $derived<TableUrlConfig>({
		prefix: urlPrefix,
		defaultPageSize,
		filterKeys: filters.map((f) => f.key)
	});
	const urlState = $derived(readTableUrlState(page.url.searchParams, urlConfig));
	const effectiveSort = $derived(urlState.sort ?? defaultSort);
	const sorting = $derived(parseSort(effectiveSort, sortKeys.byKey));
	const columnFilters = $derived<ColumnFiltersState>(
		Object.entries(urlState.filters).map(([id, value]) => ({ id, value }))
	);

	// TanStack state mapped to the §12.1 list parameters
	const query = $derived.by<TableQuery>(() => {
		const q: TableQuery = { page: urlState.page, pageSize: urlState.pageSize };
		if (effectiveSort) q.sort = effectiveSort;
		if (urlState.search.trim()) q.search = urlState.search.trim();
		for (const [key, values] of Object.entries(urlState.filters)) q[key] = values.join(',');
		return q;
	});

	// Other tables and unrelated parameters also change the URL, so fetches are keyed on the serialized query
	const queryKey = $derived(JSON.stringify(query));

	const isFiltered = $derived(
		urlState.search.trim() !== '' ||
			Object.keys(urlState.filters).length > 0 ||
			(urlState.sort !== null && urlState.sort !== defaultSort)
	);

	const table = createSvelteTable<TData>({
		get data() {
			return rows;
		},
		get columns() {
			return tableColumns;
		},
		get rowCount() {
			return total;
		},
		getRowId: (row) => getRowId(row),
		getCoreRowModel: getCoreRowModel(),
		manualPagination: true,
		manualSorting: true,
		manualFiltering: true,
		enableSortingRemoval: false,
		state: {
			get pagination() {
				return { pageIndex: urlState.page - 1, pageSize: urlState.pageSize };
			},
			get sorting() {
				return sorting;
			},
			get columnFilters() {
				return columnFilters;
			},
			get globalFilter() {
				return urlState.search;
			}
		},
		onPaginationChange: (updater) => {
			const current = { pageIndex: urlState.page - 1, pageSize: urlState.pageSize };
			const next = resolve(updater, current);

			// A different page size shifts every page boundary, so it starts over at the first page
			const pageNumber = next.pageSize !== current.pageSize ? 1 : next.pageIndex + 1;
			navigate({ ...urlState, page: pageNumber, pageSize: next.pageSize });
		},
		onSortingChange: (updater) => {
			const sort = formatSort(resolve(updater, sorting), sortKeys.byColumnId);
			navigate({ ...urlState, page: 1, sort: sort && sort !== defaultSort ? sort : null });
		},
		onColumnFiltersChange: (updater) => {
			const next = resolve(updater, columnFilters);
			const filterValues = Object.fromEntries(
				next
					.filter((f) => Array.isArray(f.value) && f.value.length > 0)
					.map((f) => [f.id, f.value as string[]])
			);
			navigate({ ...urlState, page: 1, filters: filterValues });
		},
		onGlobalFilterChange: (updater) => {
			const search = String(resolve(updater, urlState.search) ?? '');

			// Typing replaces the history entry, so back doesn't step through every search keystroke
			navigate({ ...urlState, page: 1, search }, true);
		}
	});

	// Fetch whenever the query changes
	$effect(() => {
		void queryKey;
		untrack(() => void load(query));
	});

	// Search input with its own state, so typing stays responsive while the URL only changes after the debounce
	let searchInput = $state(untrack(() => urlState.search));
	const applySearch = debounced(
		(value: string) => table.setGlobalFilter(value),
		SEARCH_DEBOUNCE_MS
	);

	// Back/forward navigation changes the search from outside the input
	$effect(() => {
		const search = urlState.search;
		untrack(() => {
			if (!applySearch.pending) searchInput = search;
		});
	});

	async function load(q: TableQuery) {
		const seq = ++requestSeq;
		loading = true;

		const result = await tryCatch(fetchPage(q));

		// A newer request superseded this one while it was in flight
		if (seq !== requestSeq) return;
		loading = false;

		if (result.error) {
			loadError = result.error;
			apiErrorToast(result.error, 'Failed to load data');
			return;
		}

		loadError = null;
		rows = result.data.items ?? [];
		total = result.data.total;
		loaded = true;

		// Deleting the last rows of the last page, or a stale link, can point past the end, so jump to the last page that exists
		const lastPage = Math.max(1, Math.ceil(total / q.pageSize));
		if (rows.length === 0 && q.page > lastPage) {
			navigate({ ...urlState, page: lastPage }, true);
		}
	}

	function navigate(next: TableUrlState, replace = false) {
		const url = new URL(page.url);
		writeTableUrlState(url.searchParams, next, urlConfig);
		if (url.search === page.url.search) return;
		void goto(url, { replaceState: replace, keepFocus: true, noScroll: true });
	}

	function onSearchInput(value: string) {
		searchInput = value;
		applySearch(value);
	}

	function clearSearch() {
		applySearch.cancel();
		searchInput = '';
		table.setGlobalFilter('');
	}

	function setFilter(key: string, values: string[]) {
		table.setColumnFilters((old) => [
			...old.filter((f) => f.id !== key),
			...(values.length > 0 ? [{ id: key, value: values }] : [])
		]);
	}

	function resetView() {
		applySearch.cancel();
		searchInput = '';
		navigate({ ...urlState, page: 1, sort: null, search: '', filters: {} });
	}

	// Reloads the current page, e.g. after creating or deleting a row
	export function refresh() {
		return load(untrack(() => query));
	}

	// Patches a row on the current page in place, without refetching or reordering (used for live updates)
	export function updateRow(id: string, patch: Partial<TData> | ((row: TData) => TData)) {
		rows = rows.map((row) => {
			if (getRowId(row) !== id) return row;
			return typeof patch === 'function' ? patch(row) : { ...row, ...patch };
		});
	}

	// Returns the rows of the current page, e.g. to decide whether a live event concerns a visible row
	export function getRows() {
		return rows;
	}

	// Returns the list parameters of the current view, e.g. to only auto-refresh the first page in the default sort
	export function getQuery(): TableQuery {
		return untrack(() => query);
	}

	// Opens the row's link unless the click landed on something interactive inside the row or selected text
	function onRowClick(event: MouseEvent, row: TData) {
		if (!rowHref) return;
		const target = event.target as HTMLElement;
		if (target.closest('a, button, input, select, textarea, [role="button"], [role="checkbox"]')) {
			return;
		}
		if (window.getSelection()?.toString()) return;

		const href = rowHref(row);
		if (event.metaKey || event.ctrlKey || event.button === 1) {
			window.open(href, '_blank');
			return;
		}
		void goto(href);
	}

	function resolve<T>(updater: Updater<T>, current: T): T {
		return typeof updater === 'function' ? (updater as (old: T) => T)(current) : updater;
	}

	function columnId(column: ColumnDef<TData, any>): string | undefined {
		if (column.id) return column.id;
		if ('accessorKey' in column) return String(column.accessorKey).replaceAll('.', '_');
		return undefined;
	}

	function ariaSort(sorted: false | 'asc' | 'desc') {
		if (sorted === 'asc') return 'ascending';
		if (sorted === 'desc') return 'descending';
		return undefined;
	}
</script>

<div class={cn('flex flex-col gap-3', className)}>
	{#if searchable || filters.length > 0 || actions}
		<div class="flex flex-wrap items-center gap-2">
			{#if searchable}
				<InputGroup.Root class="w-full sm:w-64">
					<InputGroup.Addon>
						<SearchIcon />
					</InputGroup.Addon>
					<InputGroup.Input
						type="search"
						class="[&::-webkit-search-cancel-button]:hidden"
						placeholder={searchPlaceholder}
						aria-label={searchPlaceholder}
						value={searchInput}
						oninput={(e) => onSearchInput(e.currentTarget.value)}
						onkeydown={(e) => e.key === 'Escape' && clearSearch()}
					/>
					{#if searchInput}
						<InputGroup.Addon align="inline-end">
							<InputGroup.Button size="icon-xs" aria-label="Clear search" onclick={clearSearch}>
								<XIcon />
							</InputGroup.Button>
						</InputGroup.Addon>
					{/if}
				</InputGroup.Root>
			{/if}
			{#each filters as filter (filter.key)}
				{#if filter.control}
					{@render filter.control({
						selected: urlState.filters[filter.key] ?? [],
						onChange: (values) => setFilter(filter.key, values)
					})}
				{:else}
					<DataTableFacetedFilter
						{filter}
						selected={urlState.filters[filter.key] ?? []}
						onChange={(values) => setFilter(filter.key, values)}
					/>
				{/if}
			{/each}
			{#if isFiltered}
				<Button variant="ghost" onclick={resetView}>
					Reset
					<XIcon data-icon="inline-end" />
				</Button>
			{/if}
			{#if actions}
				<div class="ml-auto flex items-center gap-2">
					{@render actions()}
				</div>
			{/if}
		</div>
	{/if}

	<div class="bg-card ring-border overflow-hidden rounded-lg shadow-xs ring-1">
		<Table.Root aria-label={label} aria-busy={loading}>
			<Table.Header>
				{#each table.getHeaderGroups() as headerGroup (headerGroup.id)}
					<Table.Row class="hover:bg-transparent">
						{#each headerGroup.headers as header (header.id)}
							{@const def = header.column.columnDef}
							<Table.Head
								colspan={header.colSpan}
								aria-sort={ariaSort(header.column.getIsSorted())}
								class={cn('text-foreground h-10 text-sm font-semibold', def.meta?.headerClass)}
							>
								{#if !header.isPlaceholder}
									{#if header.column.getCanSort() && typeof def.header === 'string'}
										<DataTableSortHeader column={header.column} label={def.header} />
									{:else}
										<FlexRender content={def.header} context={header.getContext()} />
									{/if}
								{/if}
							</Table.Head>
						{/each}
					</Table.Row>
				{/each}
			</Table.Header>
			<Table.Body
				class={cn('transition-opacity duration-200', loading && loaded && 'opacity-60 delay-150')}
			>
				{#if !loaded && !loadError}
					{#each Array.from({ length: SKELETON_ROWS }, (_, i) => i) as i (i)}
						<Table.Row class="hover:bg-transparent" data-skeleton>
							{#each table.getVisibleLeafColumns() as column (column.id)}
								<Table.Cell class={column.columnDef.meta?.cellClass}>
									<Skeleton class="h-4 w-full max-w-40" />
								</Table.Cell>
							{/each}
						</Table.Row>
					{/each}
				{:else if !loaded && loadError}
					<Table.Row class="hover:bg-transparent">
						<Table.Cell colspan={tableColumns.length} class="h-32 text-center">
							<div class="flex flex-col items-center gap-3">
								<p class="text-muted-foreground">
									{getErrorMessage(loadError, 'Failed to load data')}
								</p>
								<Button variant="outline" size="sm" onclick={refresh}>Try again</Button>
							</div>
						</Table.Cell>
					</Table.Row>
				{:else if rows.length === 0}
					<Table.Row class="hover:bg-transparent">
						<Table.Cell colspan={tableColumns.length} class="h-32 text-center whitespace-normal">
							{#if isFiltered}
								<div class="flex flex-col items-center gap-3">
									<p class="text-muted-foreground">No results match your search or filters</p>
									<Button variant="outline" size="sm" onclick={resetView}>Clear filters</Button>
								</div>
							{:else if empty}
								{@render empty()}
							{:else}
								<p class="text-muted-foreground">Nothing here yet</p>
							{/if}
						</Table.Cell>
					</Table.Row>
				{:else}
					{#each table.getRowModel().rows as row (row.id)}
						<Table.Row
							data-row-id={row.id}
							class={cn(rowHref && 'cursor-pointer')}
							onclick={(e) => onRowClick(e, row.original)}
							onauxclick={(e) => e.button === 1 && onRowClick(e, row.original)}
						>
							{#each row.getVisibleCells() as cell (cell.id)}
								<Table.Cell class={cell.column.columnDef.meta?.cellClass}>
									<FlexRender content={cell.column.columnDef.cell} context={cell.getContext()} />
								</Table.Cell>
							{/each}
						</Table.Row>
					{/each}
				{/if}
			</Table.Body>
		</Table.Root>
	</div>

	{#if loaded}
		<DataTablePagination {table} {pageSizes} />
	{/if}
</div>
