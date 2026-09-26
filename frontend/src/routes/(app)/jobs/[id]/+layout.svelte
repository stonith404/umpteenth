<script lang="ts">
	import RunNowDialog from '$lib/components/jobs/run-now-dialog.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import PageTabs from '$lib/components/page-tabs.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { modeIconClasses } from '$lib/components/runs/mode-badge.svelte';
	import { modeIcons, modeLabel, statusLabel, type RunMode } from '$lib/components/runs/run-meta';
	import StatusBadge, {
		statusTone,
		statusToneClasses
	} from '$lib/components/runs/status-badge.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { relativeTimeClock } from '$lib/utils/clock.svelte';
	import { GRADUATION_OFF_NOTE, jobScheduleParts } from '$lib/utils/job-util';
	import { cn } from '$lib/utils/style';
	import { invalidateAfterNavigation, subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import CalendarClockIcon from '@lucide/svelte/icons/calendar-clock';
	import CalendarOffIcon from '@lucide/svelte/icons/calendar-off';
	import CircleDashedIcon from '@lucide/svelte/icons/circle-dashed';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import GraduationCapIcon from '@lucide/svelte/icons/graduation-cap';
	import PinIcon from '@lucide/svelte/icons/pin';
	import PlayIcon from '@lucide/svelte/icons/play';
	import { onMount } from 'svelte';
	import type { LayoutProps } from './$types';
	import { jobPageTabs } from './job-tabs';

	let { data, children }: LayoutProps = $props();

	const job = $derived(data.job);
	const schedule = $derived(jobScheduleParts(job));
	const tabs = $derived(jobPageTabs(job.id));

	// A job that may not graduate never leaves the agent, so the mode gets a note saying why it stays put
	const graduationNote = $derived(job.graduate ? null : GRADUATION_OFF_NOTE);

	// Every fact is a small muted icon and a few words, the same as the facts in the run header
	const factClass = 'inline-flex min-w-0 items-center gap-1.5';
	const iconClass = 'size-4 shrink-0';

	// Reads as a sentence after 'Last run', e.g. 'Last run timed out 2 hours ago'
	// A run that is provisioning or running says when it started, since 'Last run running' reads badly
	function lastRunVerb(status: string) {
		return statusTone(status) === 'live' ? 'started' : statusLabel(status).toLowerCase();
	}

	let runNowOpen = $state(false);

	// The scheduler starts runs while the page is open, so the header reloads the job to keep its last run, next run and mode current
	onMount(() =>
		subscribeWorkspaceEvents({
			onRun: (event) => {
				if (event.jobId === job.id) void invalidateAfterNavigation('app:job');
			},
			onReflection: (event) => {
				if (event.jobId === job.id && event.status !== 'pending')
					void invalidateAfterNavigation('app:job');
			},
			onReconnect: () => void invalidateAfterNavigation('app:job')
		})
	);
</script>

{#snippet scheduleFact()}
	{#if schedule}
		<!-- The tooltip opens below, since above it would cover the job's name -->
		<Tooltip.Root>
			<Tooltip.Trigger>
				{#snippet child({ props })}
					<span {...props} class={cn(factClass, 'cursor-default')}>
						<CalendarClockIcon class={iconClass} aria-hidden="true" />
						<span
							>{schedule.label}{#if schedule.zone}&nbsp;· {schedule.zone}{/if}</span
						>
					</span>
				{/snippet}
			</Tooltip.Trigger>
			<Tooltip.Content side="bottom" align="start" class="font-mono text-xs">
				{schedule.detail}
			</Tooltip.Content>
		</Tooltip.Root>
	{:else}
		<span class={factClass}>
			<CalendarOffIcon class={iconClass} aria-hidden="true" />
			On demand
		</span>
	{/if}
{/snippet}

{#snippet nextRunFact()}
	<span class={factClass}>
		<ClockIcon class={iconClass} aria-hidden="true" />
		{#if job.nextRunAt && job.nextRunAt > relativeTimeClock.now}
			<span>Next run <RelativeTime value={job.nextRunAt} side="bottom" /></span>
		{:else if job.nextRunAt}
			<!-- Between the scheduled time and the run showing up, a relative time would read "Next run 1 minute ago" -->
			Next run due now
		{:else}
			No upcoming run
		{/if}
	</span>
{/snippet}

{#snippet lastRunFact()}
	{#if job.lastRun}
		{@const tone = statusTone(job.lastRun.status)}
		<a href="/runs/{job.lastRun.id}" class={cn(factClass, 'link-underline')}>
			<!-- The words say the outcome, so the icon is only decoration for screen readers -->
			<span class="inline-flex" aria-hidden="true">
				<StatusBadge status={job.lastRun.status} iconOnly class="size-4" />
			</span>
			<span>
				Last run
				<span class={cn((tone === 'danger' || tone === 'warning') && statusToneClasses[tone].label)}
					>{lastRunVerb(job.lastRun.status)}</span
				>
				<RelativeTime value={job.lastRun.queuedAt} interactive={false} />
			</span>
		</a>
	{:else}
		<span class={factClass}>
			<CircleDashedIcon class={iconClass} aria-hidden="true" />
			Never run
		</span>
	{/if}
{/snippet}

{#snippet modeFact(mode: string)}
	{@const ModeIcon = modeIcons[mode as RunMode]}
	<span class={factClass}>
		{#if ModeIcon}
			<ModeIcon class={cn(iconClass, modeIconClasses[mode as RunMode])} aria-hidden="true" />
		{/if}
		<span>Runs as {modeLabel(mode)}</span>
		{#if graduationNote}
			<Tooltip.Root>
				<Tooltip.Trigger>
					{#snippet child({ props })}
						<span {...props} class="inline-flex cursor-default">
							<PinIcon class="size-3.5" aria-hidden="true" />
							<span class="sr-only">{graduationNote}</span>
						</span>
					{/snippet}
				</Tooltip.Trigger>
				<Tooltip.Content side="bottom" class="max-w-64">{graduationNote}</Tooltip.Content>
			</Tooltip.Root>
		{/if}
		{#if job.demoted}
			<Tooltip.Root>
				<Tooltip.Trigger>
					{#snippet child({ props })}
						<span {...props} class="inline-flex cursor-default">
							<Badge variant="warning">Demoted</Badge>
						</span>
					{/snippet}
				</Tooltip.Trigger>
				<Tooltip.Content side="bottom" class="max-w-64">
					The main script fell back to the agent twice in a row, so runs are Assisted until the job
					graduates again
				</Tooltip.Content>
			</Tooltip.Root>
		{/if}
	</span>
{/snippet}

{#snippet learningFact()}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<a {...props} href="/jobs/{job.id}/settings" class={cn(factClass, 'link-underline')}>
					<GraduationCapIcon class={iconClass} aria-hidden="true" />
					Learning off
				</a>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content side="bottom" class="max-w-64">
			Runs don't update the playbook. Turn learning on in the settings.
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

<div class="flex flex-col gap-6">
	<PageHeader title={job.name}>
		{#snippet meta()}
			{@render scheduleFact()}
			{#if job.cron}
				{@render nextRunFact()}
			{/if}
			{@render lastRunFact()}
			{#if job.nextMode}
				{@render modeFact(job.nextMode)}
			{/if}
			{#if !job.selfImprove}
				{@render learningFact()}
			{/if}
		{/snippet}
		{#snippet actions()}
			<Button onclick={() => (runNowOpen = true)}>
				<PlayIcon data-icon="inline-start" />
				Run now
			</Button>
		{/snippet}
	</PageHeader>

	<PageTabs {tabs} label="Job sections" panelClass="flex flex-col gap-6">
		<!-- Moving to another job on the same tab remounts the page, so it never shows the previous job's tables or forms -->
		{#key job.id}
			{@render children()}
		{/key}
	</PageTabs>
</div>

<RunNowDialog bind:open={runNowOpen} {job} />
