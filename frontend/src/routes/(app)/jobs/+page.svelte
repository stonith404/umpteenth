<script lang="ts">
	import type { JobListItem } from '$lib/api/types';
	import {
		DataTable,
		renderSnippet,
		type TableFilterControlProps,
		type TableQuery
	} from '$lib/components/data-table';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import ModeBadge from '$lib/components/runs/mode-badge.svelte';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import * as Select from '$lib/components/ui/select';
	import { Switch } from '$lib/components/ui/switch';
	import JobService from '$lib/services/job-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { scheduleLabel } from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import BriefcaseIcon from '@lucide/svelte/icons/briefcase';
	import CalendarClockIcon from '@lucide/svelte/icons/calendar-clock';
	import PinIcon from '@lucide/svelte/icons/pin';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';

	const jobService = new JobService();

	let dataTable: ReturnType<typeof DataTable<JobListItem>> | undefined = $state();

	// Jobs whose enabled switch is saving, so the switch can't be toggled twice
	let toggling = $state<Record<string, boolean>>({});

	const enabledOptions = [
		{ value: 'true', label: 'Enabled' },
		{ value: 'false', label: 'Paused' }
	];

	const columns: ColumnDef<JobListItem>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'max-w-72' },
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		},
		{
			id: 'schedule',
			header: 'Schedule',
			cell: ({ row }) => renderSnippet(scheduleCell, row.original)
		},
		{
			id: 'mode',
			header: 'Mode',
			cell: ({ row }) => renderSnippet(modeCell, row.original)
		},
		{
			id: 'lastRun',
			header: 'Last run',
			cell: ({ row }) => renderSnippet(lastRunCell, row.original)
		},
		{
			accessorKey: 'nextRunAt',
			header: 'Next run',
			meta: { sortKey: 'nextRunAt' },
			cell: ({ row }) => renderSnippet(nextRunCell, row.original)
		},
		{
			accessorKey: 'runCount',
			header: 'Runs',
			meta: { sortKey: 'runCount', headerClass: 'text-right', cellClass: 'text-right numeric' }
		},
		{
			accessorKey: 'enabled',
			header: 'Enabled',
			meta: { sortKey: 'enabled', headerClass: 'w-0', cellClass: 'w-0' },
			cell: ({ row }) => renderSnippet(enabledCell, row.original)
		}
	];

	// The API filters by one enabled state, so selecting both states is the same as no filter
	function fetchPage(query: TableQuery) {
		const { enabled, ...rest } = query;
		const values = String(enabled ?? '')
			.split(',')
			.filter(Boolean);
		return jobService.list({
			...rest,
			enabled: values.length === 1 ? (values[0] as 'true' | 'false') : undefined
		});
	}

	// The update endpoint merges, so only the enabled state is sent
	async function setEnabled(job: JobListItem, enabled: boolean) {
		toggling[job.id] = true;
		dataTable?.updateRow(job.id, { enabled });

		const result = await tryCatch(jobService.update(job.id, { enabled }));
		toggling[job.id] = false;

		if (result.error) {
			dataTable?.updateRow(job.id, { enabled: !enabled });
			apiErrorToast(result.error, 'Failed to update the job');
			return;
		}
		dataTable?.updateRow(job.id, {
			enabled: result.data.enabled,
			nextRunAt: result.data.nextRunAt
		});
		toast.success(enabled ? `Enabled "${job.name}"` : `Paused "${job.name}"`);
	}

	// Live run events keep the last-run column current without reloading the whole table
	onMount(() =>
		subscribeWorkspaceEvents({
			onRun: (event) => {
				const row = dataTable?.getRows().find((job) => job.id === event.jobId);
				if (!row || !event.runId) return;
				if (row.lastRun?.id === event.runId && event.status) {
					const lastRun = { ...row.lastRun, status: event.status };
					dataTable?.updateRow(row.id, { lastRun });
				} else {
					void dataTable?.refresh();
				}
			},
			onReconnect: () => void dataTable?.refresh()
		})
	);
</script>

{#snippet nameCell(job: JobListItem)}
	<a href="/jobs/{job.id}" class="block truncate font-medium hover:underline">{job.name}</a>
{/snippet}

{#snippet scheduleCell(job: JobListItem)}
	{@const label = scheduleLabel(job)}
	{#if label}
		<span class="inline-flex items-center gap-1.5" title={job.cron ?? undefined}>
			<CalendarClockIcon class="text-muted-foreground size-3.5 shrink-0" />
			{label}
		</span>
	{:else}
		<span class="text-muted-foreground">On demand</span>
	{/if}
{/snippet}

{#snippet modeCell(job: JobListItem)}
	<span class="inline-flex items-center gap-1.5">
		{#if job.lastMode}
			<ModeBadge mode={job.lastMode} />
		{:else}
			<span class="text-muted-foreground">—</span>
		{/if}
		{#if job.modePin}
			<span title="Pinned to {job.modePin}" class="text-muted-foreground">
				<PinIcon class="size-3.5" />
				<span class="sr-only">Pinned to {job.modePin}</span>
			</span>
		{/if}
	</span>
{/snippet}

{#snippet lastRunCell(job: JobListItem)}
	{#if job.lastRun}
		<!-- One flex row, so the badge, the run number and the time share a center line instead of drifting apart on their baselines -->
		<span class="inline-flex items-center gap-3">
			<a href="/runs/{job.lastRun.id}" class="inline-flex items-center gap-2 hover:underline">
				<StatusBadge status={job.lastRun.status} />
				<span class="text-muted-foreground numeric text-xs">#{job.lastRun.number}</span>
			</a>
			<span class="text-muted-foreground text-xs">
				<RelativeTime value={job.lastRun.queuedAt} />
			</span>
		</span>
	{:else}
		<span class="text-muted-foreground">Never run</span>
	{/if}
{/snippet}

{#snippet nextRunCell(job: JobListItem)}
	{#if !job.enabled && job.cron}
		<span class="text-muted-foreground">Paused</span>
	{:else if job.nextRunAt}
		<RelativeTime value={job.nextRunAt} />
	{:else}
		<span class="text-muted-foreground">—</span>
	{/if}
{/snippet}

{#snippet enabledCell(job: JobListItem)}
	<Switch
		checked={job.enabled}
		disabled={toggling[job.id]}
		aria-label="{job.enabled ? 'Pause' : 'Enable'} {job.name}"
		onCheckedChange={(checked) => setEnabled(job, checked)}
	/>
{/snippet}

{#snippet enabledFilter({ selected, onChange }: TableFilterControlProps)}
	<Select.Root
		type="single"
		value={selected.length === 1 ? selected[0] : 'all'}
		onValueChange={(value) => onChange(value === 'all' ? [] : [value])}
	>
		<Select.Trigger class="w-32" aria-label="Filter by state">
			{enabledOptions.find((o) => o.value === selected[0] && selected.length === 1)?.label ??
				'All jobs'}
		</Select.Trigger>
		<Select.Content>
			<Select.Item value="all" label="All jobs" />
			{#each enabledOptions as option (option.value)}
				<Select.Item value={option.value} label={option.label} />
			{/each}
		</Select.Content>
	</Select.Root>
{/snippet}

<svelte:head>
	<title>Jobs · Umpteenth</title>
</svelte:head>

<PageHeader title="Jobs" description="Agentic jobs described in plain language.">
	{#snippet actions()}
		<Button href="/jobs/new">
			<PlusIcon data-icon="inline-start" />
			New job
		</Button>
	{/snippet}
</PageHeader>

<DataTable
	bind:this={dataTable}
	label="Jobs"
	{columns}
	{fetchPage}
	getRowId={(job) => job.id}
	rowHref={(job) => `/jobs/${job.id}`}
	defaultSort="name"
	searchPlaceholder="Search jobs"
	filters={[{ key: 'enabled', label: 'State', options: enabledOptions, control: enabledFilter }]}
>
	{#snippet empty()}
		<Empty.Root class="py-6">
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
				<Button href="/jobs/new">
					<PlusIcon data-icon="inline-start" />
					New job
				</Button>
			</Empty.Content>
		</Empty.Root>
	{/snippet}
</DataTable>
