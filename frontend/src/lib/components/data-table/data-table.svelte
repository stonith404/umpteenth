<script lang="ts" module>
	import type { TableBreakpoint } from './types';

	// Literal class names per breakpoint, so Tailwind finds them in the source
	const HIDE_BELOW: Record<TableBreakpoint, string> = {
		sm: 'hidden sm:table-cell',
		md: 'hidden md:table-cell',
		lg: 'hidden lg:table-cell',
		xl: 'hidden xl:table-cell',
		'2xl': 'hidden 2xl:table-cell'
	};

	// The last loaded row height and row count of every table, so its skeleton matches the rows it will show on the next visit
	// It is only read when a table mounts, so it is a plain cache rather than reactive state
	// eslint-disable-next-line svelte/prefer-svelte-reactivity
	const skeletonMemory = new Map<string, { height: number; count: number }>();
</script>

<script lang="ts" generics="TData">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as InputGroup from '$lib/components/ui/input-group';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Table from '$lib/components/ui/table';
	import { debounced } from '$lib/utils/debounce-util';
	import {
		apiErrorToast,
		getErrorMessage,
		isSessionError,
		redirectToLogin
	} from '$lib/utils/error-util';
	import { cn } from '$lib/utils/style';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import {
		getCoreRowModel,
		type Column,
		type ColumnDef,
		type ColumnFiltersState,
		type Updater
	} from '@tanstack/table-core';
	import SearchIcon from '@lucide/svelte/icons/search';
	import XIcon from '@lucide/svelte/icons/x';
	import { onDestroy, untrack, type Snippet } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
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
		// Column definitions; see the ColumnMeta fields in types.ts for sorting, alignment, responsive hiding and pinning
		columns: ColumnDef<TData, any>[];
		// Loads one page from the server, usually a service method such as `apiTokenService.list`
		fetchPage: (query: TableQuery) => Promise<TablePage<TData>>;
		getRowId: (row: TData) => string;
		// The server's default sort in its own syntax (e.g. `-createdAt`), shown as the active sort when the URL has none
		defaultSort?: string;
		// Every table pages by 25, so leave this and `pageSizes` at their defaults unless a table has a strong reason not to
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
		// Makes whole rows run this on click (e.g. open a sheet), for rows that don't navigate; `rowHref` wins when both are set
		onRowClick?: (row: TData) => void;
		// Extra toolbar content on the right, e.g. a "Create" button
		actions?: Snippet;
		// Adds a checkbox to every row, and while rows are checked the toolbar shows `selectionActions` for them instead of the search and filters
		selectable?: boolean;
		// Leaves the checkbox of some rows disabled, e.g. of runs that are still running
		canSelect?: (row: TData) => boolean;
		// Toolbar content for the checked rows, which receives their IDs
		selectionActions?: Snippet<[string[]]>;
		// Names a row for its checkbox, e.g. "Select Nightly report #4"
		rowLabel?: (row: TData) => string;
		// Shown when the table has no rows at all, as opposed to no rows matching the search or filters
		empty: Snippet;
		// Drops the table's own frame, for a table that sits edge to edge inside a card (`<Card.Content class="p-0">`)
		// The toolbar, the outer cells and the pager then keep the card's gutter, so they line up with the card header
		flush?: boolean;
		// Height class of the loading rows on the first visit (e.g. 'h-14' for two-line rows), later visits reuse the measured height
		skeletonRowClass?: string;
		class?: string;
	};

	let {
		columns,
		fetchPage,
		getRowId,
		defaultSort = '',
		defaultPageSize = 25,
		pageSizes = [25, 50, 100],
		filters = [],
		searchable = true,
		searchPlaceholder = 'Search…',
		urlPrefix,
		label,
		rowHref,
		onRowClick,
		actions,
		selectable = false,
		canSelect,
		selectionActions,
		rowLabel,
		empty,
		flush = false,
		skeletonRowClass = 'h-12',
		class: className
	}: Props = $props();

	const SEARCH_DEBOUNCE_MS = 300;
	const DEFAULT_SKELETON_ROWS = 5;

	// Rows are replaced wholesale on every fetch, so they don't need deep reactivity
	let rows = $state.raw<TData[]>([]);
	let total = $state(0);
	let loading = $state(false);
	let loaded = $state(false);
	let loadError = $state<unknown>(null);
	let requestSeq = 0;

	let tableRef = $state<HTMLTableElement | null>(null);
	let bodyRef = $state<HTMLTableSectionElement | null>(null);
	let overflowing = $state(false);

	const clickable = $derived(!!rowHref || !!onRowClick);

	// The checked rows, which only ever belong to the current page
	const selection = new SvelteSet<string>();
	const selectableIds = $derived(
		selectable ? rows.filter((row) => canSelect?.(row) ?? true).map((row) => getRowId(row)) : []
	);
	const allSelected = $derived(
		selectableIds.length > 0 && selectableIds.every((id) => selection.has(id))
	);
	const someSelected = $derived(!allSelected && selectableIds.some((id) => selection.has(id)));
	const memoryKey = untrack(() => `${page.route.id}|${urlPrefix ?? ''}|${label ?? ''}`);
	const skeleton = untrack(() => skeletonMemory.get(memoryKey));

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
	// TanStack only sorts columns with an accessor, so display columns get one that is never read because the server sorts
	const tableColumns = $derived(
		columns.map((column) => {
			const sortable = !!column.meta?.sortKey;
			const hasAccessor = 'accessorKey' in column || 'accessorFn' in column;
			return {
				...column,
				enableSorting: sortable,
				...(sortable && !hasAccessor ? { accessorFn: () => null } : {})
			} as ColumnDef<TData, any>;
		})
	);

	// The URL is the single source of truth, so links, reloads and back/forward all restore the same view
	const urlConfig = $derived<TableUrlConfig>({
		prefix: urlPrefix,
		defaultPageSize,
		filterKeys: filters.map((f) => f.key)
	});
	const urlState = $derived(readTableUrlState(page.url.searchParams, urlConfig));
	// A sort from the URL only counts while a column declares every key in it, so e.g. a link sorted by cost falls back to the default once the table shows tokens instead
	const effectiveSort = $derived.by(() => {
		const sort = urlState.sort;
		if (!sort) return defaultSort;
		const keys = sort.split(',').filter((part) => part.trim());
		return parseSort(sort, sortKeys.byKey).length === keys.length ? sort : defaultSort;
	});
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

	// A sort only reorders the rows, so it neither counts as a filter nor gets reset with them
	const isFiltered = $derived(
		urlState.search.trim() !== '' || Object.keys(urlState.filters).length > 0
	);

	// A table without any rows shows only its empty state, since search, filters and headers over nothing are noise
	const isEmpty = $derived(loaded && total === 0 && !isFiltered);
	const showSearch = $derived(searchable && !isEmpty);
	const showFilters = $derived(filters.length > 0 && !isEmpty);

	// When nothing matches, the message below the header offers 'Clear filters', so the toolbar doesn't repeat it as 'Reset'
	const noResults = $derived(loaded && rows.length === 0 && isFiltered);

	// Reset sits with the filters, a table with a search alone clears it with the search field's own button
	const showReset = $derived(showFilters && isFiltered && !noResults);

	// The toolbar row only renders when it has something to hold
	const showToolbar = $derived(showSearch || showFilters || !!actions || selection.size > 0);

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

	// Short lists skip the pager, it only shows once there is a second page or the user already paged or picked a page size
	const showPagination = $derived(
		loaded &&
			total > 0 &&
			(table.getPageCount() > 1 || urlState.page > 1 || urlState.pageSize !== defaultPageSize)
	);

	// Fetch whenever the query changes
	$effect(() => {
		void queryKey;
		untrack(() => void load(query));
	});

	// A new page, search, filter or sort starts without a selection, so an action never reaches rows that are out of sight
	$effect(() => {
		void queryKey;
		untrack(() => selection.clear());
	});

	// Rows that left the page on a refresh, or can no longer be selected, drop out of the selection
	$effect(() => {
		const ids = new Set(selectableIds);
		untrack(() => {
			for (const id of selection) if (!ids.has(id)) selection.delete(id);
		});
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

	// Remember how the loaded rows look, so the next visit's skeleton has the same height and count
	$effect(() => {
		const count = rows.length;
		const firstRow = bodyRef?.querySelector<HTMLElement>('tr[data-row-id]');
		if (count > 0 && firstRow) {
			skeletonMemory.set(memoryKey, { height: firstRow.offsetHeight, count });
		}
	});

	// A pinned column only needs its edge fade while the table is wider than its frame
	$effect(() => {
		const tableEl = tableRef;
		const container = tableEl?.parentElement;
		if (!tableEl || !container) return;
		const measure = () => (overflowing = container.scrollWidth > container.clientWidth + 1);
		const observer = new ResizeObserver(measure);
		observer.observe(container);
		observer.observe(tableEl);
		measure();
		return () => observer.disconnect();
	});

	// A pending search or a late response would otherwise write this table's parameters into whatever page the user moved on to
	onDestroy(() => {
		applySearch.cancel();
		requestSeq++;
	});

	async function load(q: TableQuery) {
		const seq = ++requestSeq;
		loading = true;

		const result = await tryCatch(fetchPage(q));

		// A newer request superseded this one while it was in flight
		if (seq !== requestSeq) return;
		loading = false;

		// A failed first load has its own inline message, the toast is only for a failed refresh behind rows that stay visible
		if (result.error) {
			// Signing in again is the only fix for a lost session, so it goes to the login page instead of showing either
			if (isSessionError(result.error) && redirectToLogin()) return;
			if (loaded) apiErrorToast(result.error, 'Failed to load data');
			loadError = result.error;
			return;
		}

		loadError = null;
		rows = result.data.items;
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

	// Clears the search and filters but keeps the sort, which the column headers change back
	function resetView() {
		applySearch.cancel();
		searchInput = '';
		navigate({ ...urlState, page: 1, search: '', filters: {} });
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

	// Unchecks every row, e.g. after an action on the selection
	export function clearSelection() {
		selection.clear();
	}

	function toggleAll(checked: boolean) {
		for (const id of selectableIds) {
			if (checked) selection.add(id);
			else selection.delete(id);
		}
	}

	function toggleRow(id: string, checked: boolean) {
		if (checked) selection.add(id);
		else selection.delete(id);
	}

	// Opens the row's link or runs its click handler, unless the click landed on something interactive inside the row or selected text
	function handleRowClick(event: MouseEvent, row: TData) {
		if (!clickable) return;
		const interactive =
			'a, button, input, select, textarea, label, [role="button"], [role="checkbox"], [role="switch"], [role="menuitem"]';
		if (event.target instanceof Element && event.target.closest(interactive)) return;
		if (window.getSelection()?.toString()) return;

		if (!rowHref) {
			if (event.button === 0) onRowClick?.(row);
			return;
		}
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

	// Right alignment comes from `meta.align`, or from a `text-right` header class of tables written before it existed
	function alignOf(column: Column<TData, unknown>) {
		const meta = column.columnDef.meta;
		if (meta?.align) return meta.align;
		return /(^|\s)text-right(\s|$)/.test(meta?.headerClass ?? '') ? 'right' : 'left';
	}

	function isPinned(column: Column<TData, unknown>) {
		const sticky = column.columnDef.meta?.sticky;
		return sticky ? sticky === 'right' : column.id === 'actions';
	}

	// The classes a column puts on both its header and its cells
	function columnClass(column: Column<TData, unknown>) {
		const hideBelow = column.columnDef.meta?.hideBelow;
		return cn(hideBelow && HIDE_BELOW[hideBelow], alignOf(column) === 'right' && 'text-right');
	}

	// The width and min-width utilities of a column's `cellClass` (e.g. `w-full` on the column that fills the row, `w-0` on one that hugs its content), which its header takes too
	// Without them a table whose search matches nothing sizes its columns by the header labels alone, so the columns jump when the rows disappear
	// Max widths stay on the cells, since `max-w-0` on a header would let its column shrink below the label
	function headerWidthClass(column: Column<TData, unknown>) {
		const cellClass = column.columnDef.meta?.cellClass;
		if (!cellClass) return '';
		return cellClass
			.split(/\s+/)
			.filter((name) => /^(?:[\w-]+:)*(?:min-)?w-/.test(name))
			.join(' ');
	}

	// A pinned column stays on the right edge with an opaque background and fades out what scrolls under it
	function pinnedClass(column: Column<TData, unknown>, element: 'head' | 'cell') {
		if (!isPinned(column)) return '';
		return cn(
			'bg-card sticky right-0 transition-colors',
			element === 'head' ? 'z-2' : 'z-1',
			element === 'cell' && clickable && 'group-hover/row:bg-layer',
			element === 'cell' && 'group-data-[state=selected]/row:bg-muted',
			overflowing &&
				'before:to-card before:pointer-events-none before:absolute before:inset-y-0 before:-left-6 before:w-6 before:bg-linear-to-r before:from-transparent',
			overflowing && element === 'cell' && clickable && 'group-hover/row:before:to-layer'
		);
	}
</script>

{#snippet stateBlock(message: string, action: string, onclick: () => void, busy = false)}
	<div class="flex flex-col items-center gap-3 px-6 py-10 text-center">
		<p class="text-muted-foreground">{message}</p>
		<Button variant="outline" size="sm" isLoading={busy} {onclick}>{action}</Button>
	</div>
{/snippet}

{#snippet filterControl(filter: TableFilter)}
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
{/snippet}

<!-- A flush table takes the gutter and the corner radius of the card around it, the gutter being smaller in a small card -->
<div
	class={cn(
		'flex flex-col gap-3',
		flush &&
			'[--table-gutter:--spacing(4)] rounded-[inherit] group-data-[size=sm]/card:[--table-gutter:--spacing(3)]',
		className
	)}
>
	{#if showToolbar}
		<!-- Everything shares one row where it fits, and the search gives up width before a filter wraps -->
		<!-- On phones the search keeps the first row with the actions, and filters that don't fit beside it get one row of their own that scrolls sideways -->
		<div
			class={cn(
				'flex flex-wrap items-center gap-2',
				flush && 'px-(--table-gutter) pt-(--table-gutter)'
			)}
		>
			{#if selection.size > 0}
				<!-- The selection takes over the toolbar, so its actions sit where the eye already is and the filters can't change the rows underneath it -->
				<div class="flex min-h-9 w-full flex-wrap items-center gap-2">
					<span class="text-sm font-medium" aria-live="polite">{selection.size} selected</span>
					<Button variant="ghost" onclick={clearSelection}>
						Clear selection
						<XIcon data-icon="inline-end" />
					</Button>
					{#if selectionActions}
						<div class="ml-auto flex shrink-0 items-center gap-2">
							{@render selectionActions([...selection])}
						</div>
					{/if}
				</div>
			{:else}
				{#if showSearch}
					<InputGroup.Root
						class="order-1 w-auto min-w-0 grow basis-36 sm:order-none sm:max-w-64 sm:basis-48"
					>
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
				{#if showFilters}
					<!-- From sm up this row and its scroller dissolve into the toolbar, so filters wrap one by one instead of dropping below the search as a block -->
					<div class="order-3 flex min-w-0 items-center gap-2 sm:order-none sm:contents">
						<!-- The padding keeps the focus rings clear of the scroller's clipping edge -->
						<div
							class="-m-0.5 flex min-w-0 items-center gap-2 overflow-x-auto p-0.5 [scrollbar-width:none] *:shrink-0 max-sm:scroll-fade-x sm:contents [&::-webkit-scrollbar]:hidden"
						>
							{#each filters as filter, i (filter.key)}
								{#if i < filters.length - 1}
									{@render filterControl(filter)}
								{:else}
									<!-- The last filter and Reset wrap as one, so Reset never lands on a row of its own -->
									<div class="flex items-center gap-2">
										{@render filterControl(filter)}
										{#if showReset}
											<Button variant="ghost" class="hidden sm:inline-flex" onclick={resetView}>
												Reset
												<XIcon data-icon="inline-end" />
											</Button>
										{/if}
									</div>
								{/if}
							{/each}
						</div>
						<!-- On phones Reset shrinks to its icon and stays outside the scroller, so it fits beside a short filter and stays in view however far the filters scroll -->
						{#if showReset}
							<Button
								variant="ghost"
								size="icon"
								class="sm:hidden"
								aria-label="Reset"
								onclick={resetView}
							>
								<XIcon />
							</Button>
						{/if}
					</div>
				{/if}
				{#if actions}
					<!-- Hidden until the snippet renders an element, since a snippet that renders nothing still leaves whitespace that would take a gap -->
					<div
						class="order-2 ml-auto hidden shrink-0 items-center gap-2 has-[*]:flex sm:order-none"
					>
						{@render actions()}
					</div>
				{/if}
			{/if}
		</div>
	{/if}

	<!-- A flush frame clips to the card's rounded corners where it reaches them, so the opaque cells of a pinned column don't square them off -->
	<div
		class={cn(
			'min-w-0',
			!flush && 'bg-card ring-border overflow-hidden rounded-lg shadow-xs ring-1',
			flush && 'overflow-hidden',
			flush && !showToolbar && 'rounded-t-[inherit]',
			flush && !showPagination && 'rounded-b-[inherit]'
		)}
	>
		{#if isEmpty}
			{@render empty()}
		{:else}
			<Table.Root
				bind:ref={tableRef}
				class={cn(
					'isolate',
					flush && '[&_tr>:first-child]:pl-(--table-gutter) [&_tr>:last-child]:pr-(--table-gutter)'
				)}
				aria-label={label}
				aria-busy={loading}
			>
				<Table.Header>
					{#each table.getHeaderGroups() as headerGroup (headerGroup.id)}
						<Table.Row>
							{#if selectable}
								<Table.Head class="w-0">
									<Checkbox
										aria-label="Select all rows"
										checked={allSelected}
										indeterminate={someSelected}
										disabled={selectableIds.length === 0}
										onCheckedChange={(checked) => toggleAll(checked)}
									/>
								</Table.Head>
							{/if}
							{#each headerGroup.headers as header (header.id)}
								{@const def = header.column.columnDef}
								<Table.Head
									colspan={header.colSpan}
									aria-sort={ariaSort(header.column.getIsSorted())}
									class={cn(
										'group/th',
										columnClass(header.column),
										headerWidthClass(header.column),
										pinnedClass(header.column, 'head'),
										def.meta?.headerClass
									)}
								>
									{#if !header.isPlaceholder}
										{#if header.column.getCanSort() && typeof def.header === 'string'}
											<DataTableSortHeader
												column={header.column}
												label={def.header}
												align={alignOf(header.column)}
											/>
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
					bind:ref={bodyRef}
					class={cn('transition-opacity duration-200', loading && loaded && 'opacity-60 delay-150')}
				>
					{#if !loaded && !loadError}
						{@const count = skeleton?.count ?? Math.min(urlState.pageSize, DEFAULT_SKELETON_ROWS)}
						{#each Array.from({ length: count }, (_, i) => i) as i (i)}
							<Table.Row
								data-skeleton
								class={skeleton ? 'h-(--skeleton-row-height)' : skeletonRowClass}
								style={skeleton ? `--skeleton-row-height: ${skeleton.height}px` : undefined}
							>
								{#if selectable}
									<Table.Cell class="w-0">
										<Skeleton class="size-4 rounded-sm" />
									</Table.Cell>
								{/if}
								{#each table.getVisibleLeafColumns() as column (column.id)}
									<Table.Cell
										class={cn(
											columnClass(column),
											pinnedClass(column, 'cell'),
											column.columnDef.meta?.cellClass
										)}
									>
										{#if isPinned(column)}
											<!-- Holds the actions column open at the width of the '⋯' button, so no column shifts when the rows arrive -->
											<div aria-hidden="true" class="invisible -my-2 ml-auto size-8"></div>
										{:else}
											<Skeleton
												class={cn(
													'inline-block h-3.5 w-full max-w-40 rounded-sm align-middle',
													i % 2 === 1 && 'max-w-28'
												)}
											/>
										{/if}
									</Table.Cell>
								{/each}
							</Table.Row>
						{/each}
					{:else if loaded && rows.length > 0}
						{#each table.getRowModel().rows as row (row.id)}
							{@const rowSelectable = canSelect?.(row.original) ?? true}
							<Table.Row
								data-row-id={row.id}
								data-state={selection.has(row.id) ? 'selected' : undefined}
								class={cn(
									(clickable || selectable) && 'group/row',
									clickable && 'hover:bg-layer cursor-pointer'
								)}
								onclick={(e) => handleRowClick(e, row.original)}
								onauxclick={(e) => e.button === 1 && handleRowClick(e, row.original)}
							>
								{#if selectable}
									<Table.Cell class="w-0">
										<Checkbox
											aria-label={`Select ${rowLabel?.(row.original) ?? 'row'}`}
											checked={selection.has(row.id)}
											disabled={!rowSelectable}
											onCheckedChange={(checked) => toggleRow(row.id, checked)}
										/>
									</Table.Cell>
								{/if}
								{#each row.getVisibleCells() as cell (cell.id)}
									<Table.Cell
										class={cn(
											'[&>*]:align-middle',
											columnClass(cell.column),
											pinnedClass(cell.column, 'cell'),
											cell.column.columnDef.meta?.cellClass
										)}
									>
										<FlexRender content={cell.column.columnDef.cell} context={cell.getContext()} />
									</Table.Cell>
								{/each}
							</Table.Row>
						{/each}
					{/if}
				</Table.Body>
			</Table.Root>

			<!-- Messages sit below the header rather than in a spanning cell, so they are sized to the frame instead of a table that scrolls sideways -->
			{#if !loaded && loadError}
				{@render stateBlock(
					getErrorMessage(loadError, 'Failed to load data'),
					'Try again',
					refresh,
					loading
				)}
			{:else if noResults}
				{@render stateBlock('No results match your search or filters', 'Clear filters', resetView)}
			{/if}
		{/if}
	</div>

	{#if showPagination}
		<DataTablePagination
			{table}
			{pageSizes}
			class={cn(flush && 'px-(--table-gutter) pb-(--table-gutter)')}
		/>
	{/if}
</div>
