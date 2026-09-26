<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { Run, RunListQuery, WorkspaceEvent } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import {
		DataTable,
		RowActions,
		actionsColumn,
		renderComponent,
		renderSnippet,
		type TableFilter,
		type TableFilterControlProps,
		type TableQuery
	} from '$lib/components/data-table';
	import { paramName } from '$lib/components/data-table/url-state';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import UsageAmount from '$lib/components/usage-amount.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import RunService from '$lib/services/run-service';
	import { relativeTimeClock } from '$lib/utils/clock.svelte';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { formatDateTime, formatRelative } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { usageFormat } from '$lib/utils/usage-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import { hasRole } from '$lib/utils/workspace-util';
	import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
	import PlayIcon from '@lucide/svelte/icons/play';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount, untrack, type Snippet } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { toast } from 'svelte-sonner';
	import DateRangeFilter from './date-range-filter.svelte';
	import { dateRangeToBounds } from './date-range';
	import ModeBadge from './mode-badge.svelte';
	import RunDuration from './run-duration.svelte';
	import {
		isLiveStatus,
		modeFilterOptions,
		statusFilterOptions,
		triggerFilterOptions,
		triggerIcon,
		triggerLabel
	} from './run-meta';
	import StatusBadge from './status-badge.svelte';

	type Props = {
		// Pins the table to one job, which names runs by number only, e.g. on the job page
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

	// One usage column, a price or tokens as the workspace chose, which also sorts by what it shows
	const usage = $derived(usageFormat());

	// Deleting erases a run's transcript and usage for everyone, so only admins get the checkboxes and the row menu
	const canDelete = $derived(hasRole(page.data.user!, 'admin'));

	// The job-scoped table names runs by number only, and drops the model, which rarely changes within one job
	const columns = $derived.by<ColumnDef<Run>[]>(() => [
		{
			id: 'status',
			// Not sortable, a 'Status' header button would sit next to the Status filter with the same name
			// Phones show only the status icon, so the header text would only widen the column there
			header: () => renderSnippet(statusHeader),
			meta: { headerClass: 'w-0', cellClass: 'w-0' },
			cell: ({ row }) => renderSnippet(statusCell, row.original)
		},
		{
			id: 'run',
			header: 'Run',
			// Across all jobs the run cell takes the leftover width, so long job names truncate instead of pushing columns off the table
			meta: { cellClass: jobId ? undefined : 'w-full max-w-0' },
			cell: ({ row }) => renderSnippet(runCell, row.original)
		},
		{
			id: 'mode',
			header: 'Mode',
			// At 1024 the sidebar leaves the table too little room for job names and the mode both
			meta: { hideBelow: jobId ? 'lg' : 'xl' },
			cell: ({ row }) =>
				renderComponent(ModeBadge, { mode: row.original.mode, appearance: 'plain' })
		},
		{
			id: 'started',
			header: 'Started',
			// In one job's table the numbers go to the right edge and the gap opens after the time
			meta: { sortKey: 'queuedAt', hideBelow: 'sm', cellClass: jobId ? 'w-full' : undefined },
			cell: ({ row }) => renderSnippet(startedCell, row.original)
		},
		{
			id: 'duration',
			header: 'Duration',
			meta: { sortKey: 'duration', align: 'right', hideBelow: 'sm' },
			cell: ({ row }) => renderComponent(RunDuration, { run: row.original, tabindex: -1 })
		},
		{
			id: 'usage',
			header: usage.label,
			meta: { sortKey: usage.sortKey, align: 'right', hideBelow: 'sm' },
			cell: ({ row }) => renderSnippet(usageCell, row.original)
		},
		...(jobId
			? []
			: [
					{
						id: 'model',
						header: 'Model',
						meta: { hideBelow: '2xl', cellClass: 'max-w-48' },
						cell: ({ row }) => renderSnippet(modelCell, row.original)
					} satisfies ColumnDef<Run>
				]),
		...(canDelete ? [actionsColumn<Run>((run) => renderSnippet(actionsCell, run))] : [])
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

		// A deleted run can't be refetched, so the page reloads and the next run moves up in its place
		if (event.deleted) {
			if (visible) scheduleTableRefresh();
			newRunIds.delete(runId);
			return;
		}

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

	function runName(run: Run) {
		return `${run.jobName} #${run.number}`;
	}

	function confirmDelete(run: Run) {
		openConfirmDialog({
			title: `Delete ${runName(run)}`,
			message:
				"The run's timeline and artifacts are deleted, and its usage no longer counts toward the workspace's totals or daily limit. This can't be undone.",
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(runService.delete(run.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the run');
						return;
					}
					toast.success(`Deleted "${runName(run)}"`);
					await dataTable?.refresh();
				}
			}
		});
	}

	function confirmDeleteMany(ids: string[]) {
		const count = ids.length === 1 ? '1 run' : `${ids.length} runs`;
		openConfirmDialog({
			title: `Delete ${count}`,
			message:
				"Their timelines and artifacts are deleted, and their usage no longer counts toward the workspace's totals or daily limit. This can't be undone.",
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(runService.deleteMany(ids));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the runs');
						return;
					}

					// Runs that started running or learning in the meantime are kept, which the toast says rather than hiding
					const deleted = result.data.deleted?.length ?? 0;
					const skipped = result.data.skipped?.length ?? 0;
					const message = `Deleted ${deleted === 1 ? '1 run' : `${deleted} runs`}`;
					if (skipped > 0) {
						toast.warning(message, {
							description:
								skipped === 1
									? '1 run was kept because it is still running or learning.'
									: `${skipped} runs were kept because they are still running or learning.`
						});
					} else {
						toast.success(message);
					}
					dataTable?.clearSelection();
					await dataTable?.refresh();
				}
			}
		});
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

{#snippet statusHeader()}
	<span class="max-sm:sr-only">Status</span>
{/snippet}

{#snippet dateRangeControl(props: TableFilterControlProps)}
	<DateRangeFilter {...props} />
{/snippet}

{#snippet statusCell(run: Run)}
	<!-- Phones keep only the icon, the label stays as screen reader text so the row still says how the run went -->
	<StatusBadge status={run.status} appearance="plain" class="max-sm:[&>span:last-child]:sr-only" />
{/snippet}

{#snippet runCell(run: Run)}
	{@const name = `${run.jobName} #${run.number}`}
	<div class="inline-flex max-w-full min-w-0 flex-col">
		<!-- One link to the run, the job is a click away from the run page -->
		<!-- A long job name truncates on its own, so the run number after it stays visible -->
		<a
			href="/runs/{run.id}"
			class="inline-flex max-w-full min-w-0 items-baseline gap-1.5 font-medium link-underline"
			title={jobId ? undefined : name}
		>
			{#if jobId}
				<span class="numeric">#{run.number}</span>
			{:else}
				<span class="truncate">{run.jobName}</span>
				<span class="text-muted-foreground numeric shrink-0 font-normal max-sm:hidden"
					>#{run.number}</span
				>
			{/if}
		</a>
		<!-- Phones hide the time, duration and usage columns, so the essentials move under the name -->
		<span class="text-muted-foreground truncate text-sm sm:hidden">
			{#if !jobId}<span class="numeric">#{run.number}</span> ·{/if}
			<RelativeTime value={run.startedAt ?? run.queuedAt} interactive={false} />
			{#if run.msTotal !== null || isLiveStatus(run.status)}
				· <RunDuration {run} plain />
			{/if}
		</span>
	</div>
{/snippet}

{#snippet startedCell(run: Run)}
	{@const Icon = triggerIcon(run.trigger)}
	{@const at = run.startedAt ?? run.queuedAt}
	<!-- One tooltip for the trigger icon and the time, which says how the run started and exactly when -->
	<Tooltip.Root>
		<Tooltip.Trigger tabindex={-1}>
			{#snippet child({ props })}
				<span {...props} class="inline-flex cursor-default items-center gap-2">
					{#if Icon}
						<Icon class="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
					{/if}
					<span class="sr-only">{triggerLabel(run.trigger)},</span>
					<time datetime={new Date(at).toISOString()}
						>{formatRelative(at, relativeTimeClock.now)}</time
					>
				</span>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>
			<dl class="grid grid-cols-[auto_auto] gap-x-3 gap-y-0.5">
				<dt class="text-muted-foreground">Trigger</dt>
				<dd>{triggerLabel(run.trigger)}</dd>
				<dt class="text-muted-foreground">{run.startedAt ? 'Started' : 'Queued'}</dt>
				<dd class="numeric">{formatDateTime(at)}</dd>
			</dl>
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet usageCell(run: Run)}
	{#if run.tokIn + run.tokOut === 0 && run.cost === 0}
		<!-- No model call happened, which a zero would misstate as a free run -->
		<span class="text-muted-foreground">—</span>
	{:else}
		<UsageAmount
			usage={{ cost: run.cost, tokens: run.tokIn + run.tokOut }}
			bare
			breakdown={{
				input: run.tokIn,
				output: run.tokOut,
				cacheRead: run.tokCacheRead,
				cacheWrite: run.tokCacheWrite
			}}
			tabindex={-1}
		/>
	{/if}
{/snippet}

{#snippet modelCell(run: Run)}
	{#if run.modelLabel}
		<!-- The display name reads better in a list, the provider's model ID is one hover away -->
		<Tooltip.Root>
			<Tooltip.Trigger tabindex={-1}>
				{#snippet child({ props })}
					<span {...props} class="text-muted-foreground block cursor-default truncate">
						{run.modelLabel}
					</span>
				{/snippet}
			</Tooltip.Trigger>
			<Tooltip.Content class="font-mono text-xs">{run.modelName ?? run.modelLabel}</Tooltip.Content>
		</Tooltip.Root>
	{:else}
		<span class="text-muted-foreground">—</span>
	{/if}
{/snippet}

{#snippet actionsCell(run: Run)}
	<RowActions
		name={runName(run)}
		items={[
			{
				label: 'Delete',
				icon: Trash2Icon,
				variant: 'destructive',
				// A live run has a runner writing to it, so it has to be stopped first
				disabled: isLiveStatus(run.status),
				onSelect: () => confirmDelete(run)
			}
		]}
	/>
{/snippet}

{#snippet selectionActions(ids: string[])}
	<Button variant="destructive-outline" onclick={() => confirmDeleteMany(ids)}>
		<Trash2Icon data-icon="inline-start" />
		Delete
	</Button>
{/snippet}

<!-- Only passed while it has something to show, since an empty toolbar row would still push the table down by its gap -->
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
	skeletonRowClass="h-12 max-sm:h-15"
	actions={newRunIds.size > 0 || actions ? toolbarActions : undefined}
	selectable={canDelete}
	canSelect={(run) => !isLiveStatus(run.status)}
	rowLabel={runName}
	selectionActions={canDelete ? selectionActions : undefined}
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
