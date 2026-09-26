<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { Run, RunListQuery, WorkspaceEvent } from '$lib/api/types';
	import {
		DataTable,
		renderComponent,
		renderSnippet,
		type TableFilter,
		type TableFilterControlProps,
		type TableQuery
	} from '$lib/components/data-table';
	import { paramName } from '$lib/components/data-table/url-state';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import RunService from '$lib/services/run-service';
	import { formatMicroCost, formatTokens } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
	import PlayIcon from '@lucide/svelte/icons/play';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount, untrack, type Snippet } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import DateRangeFilter from './date-range-filter.svelte';
	import { dateRangeToBounds } from './date-range';
	import ModeBadge from './mode-badge.svelte';
	import RunDuration from './run-duration.svelte';
	import {
		modeFilterOptions,
		statusFilterOptions,
		triggerFilterOptions,
		triggerIcon,
		triggerLabel
	} from './run-meta';
	import StatusBadge from './status-badge.svelte';

	type Props = {
		// Pins the table to one job and hides the job column, e.g. on the job page
		jobId?: string;
		// Namespaces the URL parameters, required when the page shows another table
		urlPrefix?: string;
		// Accessible name of the table, also used by tests to find it
		label?: string;
		defaultPageSize?: number;
		// Extra toolbar content on the right, e.g. a "Run now" button
		actions?: Snippet;
		// Replaces the default empty state shown when there are no runs at all
		empty?: Snippet;
		class?: string;
	};

	let {
		jobId,
		urlPrefix,
		label = 'Runs',
		defaultPageSize = 25,
		actions,
		empty: emptySnippet,
		class: className
	}: Props = $props();

	const DEFAULT_SORT = '-queuedAt';
	const ROW_REFRESH_DELAY_MS = 250;
	const TABLE_REFRESH_DELAY_MS = 400;

	const runService = new RunService();

	let dataTable: ReturnType<typeof DataTable<Run>> | undefined = $state();

	// Runs created since the page was loaded that are not on the current page
	const newRunIds = new SvelteSet<string>();

	const columns = $derived.by<ColumnDef<Run>[]>(() => [
		{
			id: 'status',
			header: 'Status',
			meta: { sortKey: 'status' },
			cell: ({ row }) => renderComponent(StatusBadge, { status: row.original.status })
		},
		...(jobId
			? []
			: [
					{
						id: 'job',
						header: 'Job',
						meta: { sortKey: 'job', cellClass: 'max-w-64 truncate' },
						cell: ({ row }) => renderSnippet(jobCell, row.original)
					} satisfies ColumnDef<Run>
				]),
		{
			id: 'number',
			header: '#',
			meta: { sortKey: 'number' },
			cell: ({ row }) => renderSnippet(numberCell, row.original)
		},
		{
			id: 'mode',
			header: 'Mode',
			cell: ({ row }) => renderComponent(ModeBadge, { mode: row.original.mode })
		},
		{
			id: 'trigger',
			header: () => renderSnippet(srOnly, 'Trigger'),
			meta: { headerClass: 'w-0', cellClass: 'w-0' },
			cell: ({ row }) => renderSnippet(triggerCell, row.original.trigger)
		},
		{
			id: 'started',
			header: 'Started',
			meta: { sortKey: 'queuedAt' },
			cell: ({ row }) =>
				renderComponent(RelativeTime, { value: row.original.startedAt ?? row.original.queuedAt })
		},
		{
			id: 'duration',
			header: 'Duration',
			meta: { sortKey: 'duration' },
			cell: ({ row }) => renderComponent(RunDuration, { run: row.original })
		},
		{
			id: 'tokens',
			header: 'Tokens',
			meta: { sortKey: 'tokens', headerClass: 'text-right', cellClass: 'text-right' },
			cell: ({ row }) => renderSnippet(tokensCell, row.original)
		},
		{
			id: 'cost',
			header: 'Cost',
			meta: { sortKey: 'cost', headerClass: 'text-right', cellClass: 'text-right' },
			cell: ({ row }) => renderSnippet(costCell, row.original)
		},
		{
			id: 'model',
			header: 'Model',
			meta: { cellClass: 'text-muted-foreground max-w-48 truncate' },
			cell: ({ row }) => row.original.modelName ?? '—'
		}
	]);

	const filters: TableFilter[] = [
		{ key: 'status', label: 'Status', options: statusFilterOptions },
		{ key: 'mode', label: 'Mode', options: modeFilterOptions },
		{ key: 'trigger', label: 'Trigger', options: triggerFilterOptions },
		{ key: 'range', label: 'Date', options: [], control: dateRangeControl }
	];

	// The first page in the default sort is where new runs appear, so only that view refetches by itself
	const isDefaultView = $derived.by(() => {
		const params = page.url.searchParams;
		const pageNumber = Number.parseInt(params.get(paramName('page', urlPrefix)) ?? '1', 10);
		const sort = params.get(paramName('sort', urlPrefix));
		return (!Number.isFinite(pageNumber) || pageNumber <= 1) && (!sort || sort === DEFAULT_SORT);
	});

	// Reaching the default view shows the new runs anyway, so the pill goes away
	$effect(() => {
		if (isDefaultView) untrack(() => newRunIds.clear());
	});

	// Maps the table state to the runs endpoint, turning the date range filter into from/to bounds
	function fetchPage(query: TableQuery) {
		const { range, ...rest } = query;
		const bounds = dateRangeToBounds(typeof range === 'string' ? range.split(',') : []);
		const q = { ...rest, ...bounds } as RunListQuery;
		if (jobId) q.job = jobId;
		return runService.list(q);
	}

	// Live updates without reshuffling (PLAN §13): visible rows are patched in place, new runs raise a pill
	// Timers are bookkeeping that nothing renders, so a plain Map is intended
	// eslint-disable-next-line svelte/prefer-svelte-reactivity
	const rowTimers = new Map<string, ReturnType<typeof setTimeout>>();
	let tableTimer: ReturnType<typeof setTimeout> | undefined;

	onMount(() => {
		const unsubscribe = subscribeWorkspaceEvents({
			onRun,
			onReconnect: () => void dataTable?.refresh()
		});
		return () => {
			unsubscribe();
			for (const timer of rowTimers.values()) clearTimeout(timer);
			clearTimeout(tableTimer);
		};
	});

	function onRun(event: WorkspaceEvent) {
		const runId = event.runId;
		if (!runId || !dataTable) return;
		if (jobId && event.jobId && event.jobId !== jobId) return;

		// A visible row takes the new status right away and the remaining fields once the run is refetched
		const visible = dataTable.getRows().some((row) => row.id === runId);
		if (visible) {
			if (event.status) dataTable.updateRow(runId, { status: event.status });
			scheduleRowRefresh(runId);
			return;
		}

		// Only newly created runs matter here, status changes of runs on other pages don't
		if (event.status !== 'queued' && event.status !== 'skipped') return;
		if (isDefaultView) {
			scheduleTableRefresh();
		} else {
			newRunIds.add(runId);
		}
	}

	// Status changes come in bursts (queued, provisioning, running), so each row is refetched once per burst
	function scheduleRowRefresh(runId: string) {
		clearTimeout(rowTimers.get(runId));
		rowTimers.set(
			runId,
			setTimeout(async () => {
				rowTimers.delete(runId);
				const result = await tryCatch(runService.get(runId));
				if (result.data) dataTable?.updateRow(runId, result.data);
			}, ROW_REFRESH_DELAY_MS)
		);
	}

	function scheduleTableRefresh() {
		clearTimeout(tableTimer);
		tableTimer = setTimeout(() => {
			tableTimer = undefined;
			void dataTable?.refresh();
		}, TABLE_REFRESH_DELAY_MS);
	}

	// Shows the new runs by going to the first page in the default sort, keeping search and filters
	function showNewRuns() {
		newRunIds.clear();
		const url = new URL(page.url);
		url.searchParams.delete(paramName('page', urlPrefix));
		url.searchParams.delete(paramName('sort', urlPrefix));
		if (url.search === page.url.search) {
			void dataTable?.refresh();
			return;
		}
		void goto(url, { keepFocus: true, noScroll: true });
	}
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet dateRangeControl(props: TableFilterControlProps)}
	<DateRangeFilter {...props} />
{/snippet}

{#snippet jobCell(run: Run)}
	<a href="/jobs/{run.jobId}" class="font-medium hover:underline">{run.jobName}</a>
{/snippet}

{#snippet numberCell(run: Run)}
	<a
		href="/runs/{run.id}"
		class="numeric text-muted-foreground hover:text-foreground hover:underline"
	>
		#{run.number}
	</a>
{/snippet}

{#snippet triggerCell(trigger: string)}
	{@const Icon = triggerIcon(trigger)}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<span {...props} class="text-muted-foreground inline-flex cursor-default">
					{#if Icon}
						<Icon class="size-4" aria-hidden="true" />
					{/if}
					<span class="sr-only">{triggerLabel(trigger)}</span>
				</span>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>{triggerLabel(trigger)}</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet tokensCell(run: Run)}
	{@const total = run.tokIn + run.tokOut}
	{#if total === 0}
		<span class="text-muted-foreground">—</span>
	{:else}
		<Tooltip.Root>
			<Tooltip.Trigger>
				{#snippet child({ props })}
					<span {...props} class="numeric cursor-default">{formatTokens(total)}</span>
				{/snippet}
			</Tooltip.Trigger>
			<Tooltip.Content>
				<div class="numeric grid grid-cols-[auto_auto] gap-x-3">
					<span>In</span><span class="text-right">{formatTokens(run.tokIn)}</span>
					<span>Out</span><span class="text-right">{formatTokens(run.tokOut)}</span>
					{#if run.tokCacheRead > 0}
						<span>Cache read</span><span class="text-right">{formatTokens(run.tokCacheRead)}</span>
					{/if}
					{#if run.tokCacheWrite > 0}
						<span>Cache write</span><span class="text-right">{formatTokens(run.tokCacheWrite)}</span
						>
					{/if}
				</div>
			</Tooltip.Content>
		</Tooltip.Root>
	{/if}
{/snippet}

{#snippet costCell(run: Run)}
	<span class="numeric">{formatMicroCost(run.cost)}</span>
{/snippet}

{#snippet toolbarActions()}
	{#if newRunIds.size > 0}
		<!-- New runs never reorder the rows under the cursor, the button lets the user pull them in -->
		<Button variant="outline" onclick={showNewRuns}>
			<ArrowUpIcon data-icon="inline-start" />
			{newRunIds.size} new {newRunIds.size === 1 ? 'run' : 'runs'}
		</Button>
	{/if}
	{@render actions?.()}
{/snippet}

<DataTable
	bind:this={dataTable}
	{label}
	{columns}
	{fetchPage}
	getRowId={(run) => run.id}
	rowHref={(run) => `/runs/${run.id}`}
	defaultSort={DEFAULT_SORT}
	{defaultPageSize}
	{filters}
	{urlPrefix}
	searchPlaceholder="Search runs"
	actions={toolbarActions}
	class={className}
>
	{#snippet empty()}
		{#if emptySnippet}
			{@render emptySnippet()}
		{:else}
			<Empty.Root class="py-6">
				<Empty.Header>
					<Empty.Media variant="icon">
						<PlayIcon />
					</Empty.Media>
					<Empty.Title>No runs yet</Empty.Title>
					<Empty.Description>
						Runs appear here as soon as a job is triggered by its schedule, a webhook or by hand.
					</Empty.Description>
				</Empty.Header>
			</Empty.Root>
		{/if}
	{/snippet}
</DataTable>
