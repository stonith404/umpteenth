<script lang="ts">
	import type { JobListItem } from '$lib/api/types';
	import {
		actionsColumn,
		DataTable,
		renderSnippet,
		RowActions,
		type TableQuery
	} from '$lib/components/data-table';
	import RunHistory from '$lib/components/jobs/run-history.svelte';
	import RunNowDialog from '$lib/components/jobs/run-now-dialog.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import ModeBadge from '$lib/components/runs/mode-badge.svelte';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import JobService from '$lib/services/job-service';
	import { GRADUATION_OFF_NOTE, scheduleParts } from '$lib/utils/job-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import BriefcaseIcon from '@lucide/svelte/icons/briefcase';
	import CircleIcon from '@lucide/svelte/icons/circle';
	import PinIcon from '@lucide/svelte/icons/pin';
	import PlayIcon from '@lucide/svelte/icons/play';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount } from 'svelte';
	import type { Attachment } from 'svelte/attachments';
	import { on } from 'svelte/events';

	const jobService = new JobService();

	let dataTable: ReturnType<typeof DataTable<JobListItem>> | undefined = $state();

	// The job the run dialog is for, kept after the dialog closes so it can animate out with its content
	let runNowJob = $state<JobListItem | null>(null);
	let runNowOpen = $state(false);

	// A workspace without any jobs shows one call to action in the empty table, so the header doesn't repeat it
	let noJobs = $state(false);

	// The job fills the width, the other columns get minimum widths that fit their longest values, so they don't jump as times tick or when the skeleton gives way to rows
	// Columns make way on smaller screens, at 1024px the sidebar leaves about 684px for the whole table
	const columns: ColumnDef<JobListItem>[] = [
		{
			accessorKey: 'name',
			header: 'Job',
			// Slightly less vertical padding than the one-line cells, so the two-line rows stay 56px tall
			meta: { sortKey: 'name', headerClass: 'w-full', cellClass: 'w-full max-w-0 py-2.5' },
			cell: ({ row }) => renderSnippet(jobCell, row.original)
		},
		{
			id: 'recentRuns',
			header: 'Recent runs',
			meta: { hideBelow: 'sm', headerClass: 'min-w-56', cellClass: 'min-w-56' },
			cell: ({ row }) => renderSnippet(recentRunsCell, row.original)
		},
		{
			accessorKey: 'nextRunAt',
			header: 'Next run',
			// What runs next is what people look for, so the first click sorts the soonest run to the top
			sortDescFirst: false,
			meta: {
				sortKey: 'nextRunAt',
				hideBelow: 'md',
				headerClass: 'min-w-32',
				cellClass: 'min-w-32'
			},
			cell: ({ row }) => renderSnippet(nextRunCell, row.original)
		},
		{
			id: 'mode',
			header: 'Mode',
			meta: { hideBelow: 'xl', headerClass: 'min-w-36', cellClass: 'min-w-36' },
			cell: ({ row }) => renderSnippet(modeCell, row.original)
		},
		actionsColumn<JobListItem>((job) => renderSnippet(actionsCell, job))
	];

	async function fetchJobs(query: TableQuery) {
		const result = await jobService.list(query);
		noJobs = result.total === 0 && !query.search;
		return result;
	}

	function openRunNow(job: JobListItem) {
		runNowJob = job;
		runNowOpen = true;
	}

	// A status change of a run the row already shows is patched in place, in the strip and in the last run
	function withRunStatus(job: JobListItem, runId: string, status: string): JobListItem {
		const recentRuns = (job.recentRuns ?? []).map((run) =>
			run.id === runId ? { ...run, status } : run
		);
		const lastRun = job.lastRun?.id === runId ? { ...job.lastRun, status } : job.lastRun;
		return { ...job, recentRuns, lastRun };
	}

	// Names cut off by the column show in full on hover, names that fit get no tooltip that only repeats them
	function titleWhenTruncated(text: string): Attachment<HTMLElement> {
		return (element) =>
			on(element, 'pointerenter', () => {
				if (element.scrollWidth > element.clientWidth) element.title = text;
				else element.removeAttribute('title');
			});
	}

	// bits-ui gives every tooltip trigger the attributes of a button, which mean nothing on a span
	// The spans keep the row's pointer cursor, since a click on them still opens the job
	function spanProps(props: Record<string, unknown>) {
		return { ...props, type: undefined, disabled: undefined };
	}

	// Live run events keep the strips current without reloading the whole table
	onMount(() =>
		subscribeWorkspaceEvents({
			onRun: (event) => {
				const row = dataTable?.getRows().find((job) => job.id === event.jobId);
				const runId = event.runId;
				if (!row || !runId) return;

				// A run the strip doesn't show yet is a new one, and only the server knows what drops off the other end
				const known = row.recentRuns?.some((run) => run.id === runId);
				if (known && event.status) {
					const status = event.status;
					dataTable?.updateRow(row.id, (job) => withRunStatus(job, runId, status));
				} else {
					void dataTable?.refresh();
				}
			},
			// Reflection can graduate or demote a job, which changes the mode its next run starts in
			onReflection: (event) => {
				if (event.status === 'pending') return;
				if (dataTable?.getRows().some((job) => job.id === event.jobId)) void dataTable?.refresh();
			},
			onReconnect: () => void dataTable?.refresh()
		})
	);
</script>

{#snippet jobCell(job: JobListItem)}
	{@const schedule = scheduleParts(job)}
	<div class="flex min-w-0 flex-col">
		<div class="flex min-w-0 items-center gap-2">
			<a
				href="/jobs/{job.id}"
				class="truncate leading-5 font-medium link-underline"
				{@attach titleWhenTruncated(job.name)}
			>
				{job.name}
			</a>
		</div>

		<!-- From sm up the second line says when the job runs, since the columns beside it show how its runs went -->
		<div class="text-muted-foreground hidden min-w-0 text-sm leading-4 sm:flex">
			{#if schedule}
				<Tooltip.Root>
					<Tooltip.Trigger tabindex={-1}>
						{#snippet child({ props })}
							<span {...spanProps(props)} class="truncate">{schedule.text}</span>
						{/snippet}
					</Tooltip.Trigger>
					<!-- Beside the line rather than above it, where it would cover the job's name -->
					<Tooltip.Content side="right" align="start" mono>
						{schedule.detail}
					</Tooltip.Content>
				</Tooltip.Root>
			{:else}
				<span class="truncate">On demand</span>
			{/if}
		</div>

		<!-- On phones the other columns are hidden, so the second line sums them up: how the last run went and what comes next -->
		<div
			class="text-muted-foreground flex min-w-0 items-center gap-1.5 text-sm leading-4 sm:hidden"
		>
			{#if job.lastRun}
				<StatusBadge status={job.lastRun.status} iconOnly class="size-4" />
			{:else}
				<!-- An empty circle holds the icon's place, so the text lines up with the rows that ran -->
				<span class="inline-flex size-4 shrink-0 items-center justify-center" title="Never run">
					<CircleIcon class="text-muted-foreground/60 size-4" aria-hidden="true" />
					<span class="sr-only">Never run</span>
				</span>
			{/if}
			<span class="truncate">
				{#if job.cron && job.nextRunAt}
					Next <RelativeTime value={job.nextRunAt} interactive={false} />
				{:else if job.cron}
					No upcoming run
				{:else}
					On demand
				{/if}
			</span>
		</div>
	</div>
{/snippet}

{#snippet recentRunsCell(job: JobListItem)}
	<div class="flex items-center gap-3">
		<RunHistory runs={job.recentRuns} />
		{#if job.lastRun}
			<RelativeTime
				value={job.lastRun.queuedAt}
				interactive={false}
				class="text-muted-foreground"
			/>
		{:else}
			<span class="text-muted-foreground">Never run</span>
		{/if}
	</div>
{/snippet}

{#snippet nextRunCell(job: JobListItem)}
	<!-- Block-level like the strip beside it, so the times sit on the same line instead of an inline baseline a pixel lower -->
	<div class="flex items-center">
		{#if job.cron && job.nextRunAt}
			<RelativeTime value={job.nextRunAt} interactive={false} />
		{:else}
			<!-- On-demand jobs say so on the job's second line, so this cell stays quiet -->
			<span class="text-muted-foreground" aria-hidden="true">—</span>
			<span class="sr-only">{job.cron ? 'No upcoming run' : 'On demand'}</span>
		{/if}
	</div>
{/snippet}

{#snippet modeCell(job: JobListItem)}
	<div class="flex items-center">
		{#if !job.graduate}
			<Tooltip.Root>
				<Tooltip.Trigger tabindex={-1}>
					{#snippet child({ props })}
						<span {...spanProps(props)} class="flex items-center gap-1.5">
							<ModeBadge mode={job.nextMode} appearance="plain" />
							<PinIcon class="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
							<span class="sr-only">{GRADUATION_OFF_NOTE}</span>
						</span>
					{/snippet}
				</Tooltip.Trigger>
				<Tooltip.Content>{GRADUATION_OFF_NOTE}</Tooltip.Content>
			</Tooltip.Root>
		{:else}
			<ModeBadge mode={job.nextMode} appearance="plain" />
		{/if}
	</div>
{/snippet}

{#snippet actionsCell(job: JobListItem)}
	<RowActions
		name={job.name}
		items={[
			{ label: 'Run now', icon: PlayIcon, onSelect: () => openRunNow(job) },
			{ label: 'Settings', icon: SettingsIcon, href: `/jobs/${job.id}/settings` }
		]}
	/>
{/snippet}

<svelte:head>
	<title>Jobs · Umpteenth</title>
</svelte:head>

{#snippet createButton()}
	<Button href="/jobs/new">
		<PlusIcon data-icon="inline-start" />
		Create job
	</Button>
{/snippet}

<PageHeader
	title="Jobs"
	description="Tasks Umpteenth runs for you, on a schedule or on demand."
	actions={noJobs ? undefined : createButton}
/>

<!-- The loading rows are as tall as the two-line rows, 56px plus the hairline under each -->
<DataTable
	bind:this={dataTable}
	label="Jobs"
	{columns}
	fetchPage={fetchJobs}
	getRowId={(job) => job.id}
	rowHref={(job) => `/jobs/${job.id}`}
	defaultSort="name"
	searchPlaceholder="Search jobs"
	skeletonRowClass="h-14.25"
>
	{#snippet empty()}
		<Empty.Root size="sm">
			<Empty.Header>
				<Empty.Media variant="icon">
					<BriefcaseIcon />
				</Empty.Media>
				<Empty.Title>No jobs yet</Empty.Title>
				<Empty.Description>
					Describe a job in plain language and Umpteenth runs it in its own sandbox.
				</Empty.Description>
			</Empty.Header>
			<Empty.Content>
				{@render createButton()}
			</Empty.Content>
		</Empty.Root>
	{/snippet}
</DataTable>

{#if runNowJob}
	<RunNowDialog
		bind:open={runNowOpen}
		job={{ id: runNowJob.id, name: runNowJob.name, spec: { inputs: runNowJob.inputs ?? [] } }}
	/>
{/if}
