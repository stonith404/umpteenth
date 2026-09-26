<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { StatsOverview, StatsRange } from '$lib/api/types';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { secondClock } from '$lib/components/runs/run-duration.svelte';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Empty from '$lib/components/ui/empty';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Tabs from '$lib/components/ui/tabs';
	import StatsService from '$lib/services/stats-service';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { formatDuration, formatMicroCost } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import LayoutDashboardIcon from '@lucide/svelte/icons/layout-dashboard';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrendingDownIcon from '@lucide/svelte/icons/trending-down';
	import { onMount, untrack } from 'svelte';
	import CostByJobChart from './cost-by-job-chart.svelte';
	import KpiCard, { percentDelta, pointsDelta } from './kpi-card.svelte';
	import RunsPerDayChart from './runs-per-day-chart.svelte';

	const RANGES: { value: StatsRange; label: string; previous: string; period: string }[] = [
		{ value: '24h', label: '24h', previous: 'previous 24 hours', period: 'the last 24 hours' },
		{ value: '7d', label: '7d', previous: 'previous 7 days', period: 'the last 7 days' },
		{ value: '30d', label: '30d', previous: 'previous 30 days', period: 'the last 30 days' },
		{ value: '90d', label: '90d', previous: 'previous 90 days', period: 'the last 90 days' }
	];
	const DEFAULT_RANGE: StatsRange = '7d';
	// Run changes arrive in bursts, one reload per burst is enough for a dashboard
	const LIVE_RELOAD_DELAY_MS = 1500;

	const statsService = new StatsService();

	// The range lives in the URL, so a shared dashboard link shows the same period
	const range = $derived(
		RANGES.find((r) => r.value === page.url.searchParams.get('range')) ?? RANGES[1]
	);

	let overview = $state.raw<StatsOverview | null>(null);
	let loadError = $state<unknown>(null);
	let requestSeq = 0;

	$effect(() => {
		const value = range.value;
		untrack(() => void load(value));
	});

	async function load(value: StatsRange, quiet = false) {
		const seq = ++requestSeq;
		const result = await tryCatch(statsService.overview({ range: value }));
		if (seq !== requestSeq) return;
		if (result.error) {
			loadError = result.error;
			if (!quiet) apiErrorToast(result.error, 'Failed to load the dashboard');
			return;
		}
		loadError = null;
		overview = result.data;
	}

	let reloadTimer: ReturnType<typeof setTimeout> | undefined;
	function scheduleReload() {
		clearTimeout(reloadTimer);
		reloadTimer = setTimeout(() => void load(range.value, true), LIVE_RELOAD_DELAY_MS);
	}

	onMount(() => {
		const unsubscribe = subscribeWorkspaceEvents({
			onRun: scheduleReload,
			onReconnect: scheduleReload
		});
		return () => {
			unsubscribe();
			clearTimeout(reloadTimer);
		};
	});

	function setRange(value: string) {
		const url = new URL(page.url);
		if (value === DEFAULT_RANGE) url.searchParams.delete('range');
		else url.searchParams.set('range', value);
		void goto(url, { replaceState: true, noScroll: true, keepFocus: true });
	}

	const current = $derived(overview?.current);
	const previous = $derived(overview?.previous);
	const perDay = $derived(overview?.perDay ?? []);
	const costByJob = $derived(overview?.costByJob ?? []);
	const running = $derived(overview?.running ?? []);
	const recentFailures = $derived(overview?.recentFailures ?? []);
	const gettingCheaper = $derived(overview?.gettingCheaper ?? []);

	// A workspace that never ran anything gets a welcome instead of a wall of zeros
	const isFreshInstall = $derived(
		!!overview &&
			overview.current.runs === 0 &&
			overview.previous.runs === 0 &&
			running.length === 0 &&
			recentFailures.length === 0 &&
			gettingCheaper.length === 0 &&
			perDay.every(
				(d) => d.succeeded + d.failed + d.cancelled + d.timedOut + d.skipped + d.other === 0
			)
	);
	const hasRunsInPeriod = $derived(
		perDay.some((d) => d.succeeded + d.failed + d.cancelled + d.timedOut + d.skipped + d.other > 0)
	);
	const hasCostInPeriod = $derived(costByJob.some((c) => c.cost > 0));

	const finishedCurrent = $derived(current ? current.succeeded + current.failed : 0);
	const finishedPrevious = $derived(previous ? previous.succeeded + previous.failed : 0);
	const spendCurrent = $derived(current ? current.runCost + current.learningCost : 0);
	const spendPrevious = $derived(previous ? previous.runCost + previous.learningCost : 0);
</script>

<svelte:head>
	<title>Dashboard · Umpteenth</title>
</svelte:head>

<PageHeader title="Dashboard" description="Runs, success rate, duration and spend at a glance.">
	{#snippet actions()}
		<Tabs.Root value={range.value} onValueChange={setRange}>
			<Tabs.List aria-label="Time range">
				{#each RANGES as r (r.value)}
					<Tabs.Trigger value={r.value} class="numeric">{r.label}</Tabs.Trigger>
				{/each}
			</Tabs.List>
		</Tabs.Root>
	{/snippet}
</PageHeader>

{#if !overview && !loadError}
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-busy="true">
		{#each [0, 1, 2, 3] as i (i)}
			<Skeleton class="h-36 rounded-2xl" />
		{/each}
	</div>
	<div class="grid gap-4 lg:grid-cols-2">
		<Skeleton class="h-80 rounded-2xl" />
		<Skeleton class="h-80 rounded-2xl" />
	</div>
{:else if !overview}
	<Empty.Root class="border border-dashed py-16">
		<Empty.Header>
			<Empty.Title>The dashboard could not be loaded</Empty.Title>
			<Empty.Description>{getErrorMessage(loadError)}</Empty.Description>
		</Empty.Header>
		<Empty.Content>
			<Button variant="outline" onclick={() => load(range.value)}>Try again</Button>
		</Empty.Content>
	</Empty.Root>
{:else if isFreshInstall}
	<Empty.Root class="flex-none border border-dashed py-16" data-testid="dashboard-empty">
		<Empty.Header>
			<Empty.Media variant="icon">
				<LayoutDashboardIcon />
			</Empty.Media>
			<Empty.Title>Nothing to show yet</Empty.Title>
			<Empty.Description>
				Once jobs start running, their results, durations and costs show up here.
			</Empty.Description>
		</Empty.Header>
		<Empty.Content>
			<Button href="/jobs/new">
				<PlusIcon data-icon="inline-start" />
				Create a job
			</Button>
		</Empty.Content>
	</Empty.Root>
{:else if current && previous}
	<section class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-label="Key figures">
		<KpiCard
			label="Runs"
			value={current.runs.toLocaleString()}
			delta={percentDelta(current.runs, previous.runs, true)}
			rangeLabel={range.previous}
		>
			{#snippet footer()}
				<p class="text-muted-foreground numeric">
					{current.succeeded} succeeded · {current.failed} failed
				</p>
			{/snippet}
		</KpiCard>
		<KpiCard
			label="Success rate"
			value={finishedCurrent > 0 ? `${Math.round(current.successRate * 100)}%` : '—'}
			delta={finishedCurrent > 0 && finishedPrevious > 0
				? pointsDelta(current.successRate, previous.successRate)
				: null}
			rangeLabel={range.previous}
		>
			{#snippet footer()}
				<div class="bg-muted flex h-1.5 overflow-hidden rounded-full" aria-hidden="true">
					<span class="h-full bg-green-500" style:width="{current.successRate * 100}%"></span>
					{#if finishedCurrent > 0}
						<span class="h-full bg-red-500" style:width="{(1 - current.successRate) * 100}%"></span>
					{/if}
				</div>
			{/snippet}
		</KpiCard>
		<KpiCard
			label="p50 duration"
			value={current.runs > 0 ? formatDuration(current.p50Ms) : '—'}
			delta={current.p50Ms > 0 ? percentDelta(current.p50Ms, previous.p50Ms, false) : null}
			rangeLabel={range.previous}
		>
			{#snippet footer()}
				<p class="text-muted-foreground numeric">
					p95 {current.runs > 0 ? formatDuration(current.p95Ms) : '—'}
				</p>
			{/snippet}
		</KpiCard>
		<KpiCard
			label="Spend"
			value={formatMicroCost(spendCurrent)}
			delta={percentDelta(spendCurrent, spendPrevious, false)}
			rangeLabel={range.previous}
		>
			{#snippet footer()}
				<p class="text-muted-foreground numeric flex flex-wrap items-center gap-x-2">
					<span class="inline-flex items-center gap-1">
						<span class="size-2 rounded-full bg-sky-500"></span>
						Runs {formatMicroCost(current.runCost)}
					</span>
					<span class="inline-flex items-center gap-1">
						<span class="size-2 rounded-full bg-violet-500"></span>
						Learning {formatMicroCost(current.learningCost)}
					</span>
				</p>
			{/snippet}
		</KpiCard>
	</section>

	<section class="grid gap-4 lg:grid-cols-2" aria-label="Charts">
		<Card.Root>
			<Card.Header>
				<Card.Title>Runs per day</Card.Title>
				<Card.Description>By final status, {range.period}</Card.Description>
			</Card.Header>
			<Card.Content>
				{#if hasRunsInPeriod}
					<RunsPerDayChart {perDay} />
				{:else}
					<p class="text-muted-foreground flex h-64 items-center justify-center text-sm">
						No runs in {range.period}
					</p>
				{/if}
			</Card.Content>
		</Card.Root>
		<Card.Root>
			<Card.Header>
				<Card.Title>Cost per day</Card.Title>
				<Card.Description>By job, including learning, {range.period}</Card.Description>
			</Card.Header>
			<Card.Content>
				{#if hasCostInPeriod}
					<CostByJobChart {perDay} {costByJob} />
				{:else}
					<p class="text-muted-foreground flex h-64 items-center justify-center text-sm">
						No spend in {range.period}
					</p>
				{/if}
			</Card.Content>
		</Card.Root>
	</section>

	<section class="grid gap-4 lg:grid-cols-3" aria-label="Runs">
		<Card.Root data-testid="running-now">
			<Card.Header>
				<Card.Title>Running now</Card.Title>
				<Card.Description>
					{running.length === 0
						? 'Nothing is running'
						: `${running.length} ${running.length === 1 ? 'run' : 'runs'} in progress`}
				</Card.Description>
			</Card.Header>
			<Card.Content class="flex-1">
				{#if running.length === 0}
					<p class="text-muted-foreground text-sm">Runs appear here while they execute.</p>
				{:else}
					<ul class="-mx-2 flex flex-col">
						{#each running as run (run.id)}
							<li>
								<a
									href="/runs/{run.id}"
									class="hover:bg-muted/60 flex items-center gap-3 rounded-lg px-2 py-2 text-sm"
								>
									<StatusBadge status={run.status} iconOnly />
									<span class="min-w-0 flex-1 truncate">
										<span class="font-medium">{run.jobName}</span>
										<span class="text-muted-foreground numeric">#{run.number}</span>
									</span>
									<span class="numeric text-muted-foreground text-xs">
										{formatDuration(Math.max(0, secondClock.now - (run.startedAt ?? run.queuedAt)))}
									</span>
								</a>
							</li>
						{/each}
					</ul>
				{/if}
			</Card.Content>
		</Card.Root>

		<Card.Root data-testid="recent-failures">
			<Card.Header>
				<Card.Title>Recent failures</Card.Title>
				<Card.Description>The last runs that failed or timed out</Card.Description>
			</Card.Header>
			<Card.Content>
				{#if recentFailures.length === 0}
					<p class="text-muted-foreground text-sm">No failures, nice.</p>
				{:else}
					<ul class="-mx-2 flex flex-col">
						{#each recentFailures as run (run.id)}
							<li>
								<a
									href="/runs/{run.id}"
									class="hover:bg-muted/60 flex items-start gap-3 rounded-lg px-2 py-2 text-sm"
								>
									<StatusBadge status={run.status} iconOnly class="mt-0.5" />
									<span class="flex min-w-0 flex-1 flex-col">
										<span class="truncate">
											<span class="font-medium">{run.jobName}</span>
											<span class="text-muted-foreground numeric">#{run.number}</span>
										</span>
										{#if run.error}
											<span class="text-muted-foreground truncate text-xs">{run.error}</span>
										{/if}
									</span>
									<span class="text-muted-foreground shrink-0 text-xs">
										<RelativeTime value={run.startedAt ?? run.queuedAt} />
									</span>
								</a>
							</li>
						{/each}
					</ul>
				{/if}
			</Card.Content>
		</Card.Root>

		<Card.Root data-testid="getting-cheaper">
			<Card.Header>
				<Card.Title>Getting cheaper</Card.Title>
				<Card.Description>Cost per run, first runs vs the latest ones</Card.Description>
			</Card.Header>
			<Card.Content>
				{#if gettingCheaper.length === 0}
					<p class="text-muted-foreground text-sm">
						Jobs show up here once their recent runs cost less than their first ones.
					</p>
				{:else}
					<ul class="-mx-2 flex flex-col">
						{#each gettingCheaper as job (job.jobId)}
							<li>
								<a
									href="/jobs/{job.jobId}"
									class="hover:bg-muted/60 flex items-center gap-3 rounded-lg px-2 py-2 text-sm"
								>
									<TrendingDownIcon class="size-4 shrink-0 text-green-600 dark:text-green-400" />
									<span class="flex min-w-0 flex-1 flex-col">
										<span class="truncate font-medium">{job.jobName}</span>
										<span class="text-muted-foreground numeric text-xs">
											{formatMicroCost(job.firstCost)} → {formatMicroCost(job.recentCost)} · {job.runs}
											runs
										</span>
									</span>
									<span
										class="numeric shrink-0 text-xs font-medium text-green-600 dark:text-green-400"
									>
										−{Math.round(job.dropPct * 100)}%
									</span>
								</a>
							</li>
						{/each}
					</ul>
				{/if}
			</Card.Content>
		</Card.Root>
	</section>
{/if}
