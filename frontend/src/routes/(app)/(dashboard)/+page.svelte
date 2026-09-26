<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { StatsOverview, StatsRange, StatsTotals } from '$lib/api/types';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { timeRanges } from '$lib/components/runs/date-range';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Empty from '$lib/components/ui/empty';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Tabs from '$lib/components/ui/tabs';
	import StatsService from '$lib/services/stats-service';
	import { secondClock } from '$lib/utils/clock.svelte';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { AGENT_FAILURE_PATTERN } from '$lib/components/runs/run-meta';
	import { formatDuration, sentenceCase } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { usageFormat } from '$lib/utils/usage-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import LayoutDashboardIcon from '@lucide/svelte/icons/layout-dashboard';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrendingDownIcon from '@lucide/svelte/icons/trending-down';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import { onMount, untrack } from 'svelte';
	import CostByJobChart from './cost-by-job-chart.svelte';
	import KpiCard, { percentDelta, pointsDelta } from './kpi-card.svelte';
	import RunsPerDayChart, { bucketRuns } from './runs-per-day-chart.svelte';

	// A run in the Recent failures list
	type FailedRun = NonNullable<StatsOverview['recentFailures']>[number];

	const RANGES = timeRanges('24h', '7d', '30d', '90d');
	// The last 7 days, which the dashboard shows without a `range` parameter
	const DEFAULT_RANGE = RANGES[1];
	// Run changes arrive in bursts, one reload per burst is enough for a dashboard
	const LIVE_RELOAD_DELAY_MS = 1500;

	const statsService = new StatsService();

	// Usage shows as a price or in tokens, as the workspace chose
	const usage = $derived(usageFormat());
	// The key figures in the order of their cards, which the loading skeleton names too
	const kpiLabels = $derived(['Runs', 'Success rate', 'Median duration', usage.totalLabel]);

	// The range lives in the URL, so a shared dashboard link shows the same period
	const range = $derived(
		RANGES.find((r) => r.value === page.url.searchParams.get('range')) ?? DEFAULT_RANGE
	);

	let overview = $state.raw<StatsOverview | null>(null);
	// The range the figures on screen belong to, which lags behind the URL while a newly picked range loads
	let overviewRange = $state<StatsRange | null>(null);
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
			if (overview && overviewRange === value) {
				// The figures on screen are still this period's, so they stay and only a refresh the user asked for reports its failure
				if (!quiet) apiErrorToast(result.error, 'Failed to load the dashboard');
			} else {
				// Another period's figures must not stay under this period's labels, so the error takes their place
				overview = null;
			}
			return;
		}
		loadError = null;
		overview = result.data;
		overviewRange = value;
	}

	// Clearing the error first brings back the skeleton, so the retry shows that it is loading
	function retry() {
		loadError = null;
		void load(range.value);
	}

	// The figures of the previous range dim until the ones of the newly picked range arrive
	const pending = $derived(!!overview && overviewRange !== range.value);
	// The period the texts name is the one of the figures on screen, so they never describe numbers that haven't loaded yet
	const shownRange = $derived((overview && RANGES.find((r) => r.value === overviewRange)) || range);

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
		if (value === DEFAULT_RANGE.value) url.searchParams.delete('range');
		else url.searchParams.set('range', value);
		void goto(url, { replaceState: true, noScroll: true, keepFocus: true });
	}

	const current = $derived(overview?.current);
	const previous = $derived(overview?.previous);
	const perDay = $derived(overview?.perDay ?? []);
	const costByJob = $derived(overview?.costByJob ?? []);
	const running = $derived(overview?.running ?? []);
	const recentFailures = $derived(overview?.recentFailures ?? []);
	// Jobs whose latest runs cost less, or used fewer tokens, than their first ones
	const trend = $derived(
		(usage.unit === 'tokens' ? overview?.gettingLeaner : overview?.gettingCheaper) ?? []
	);

	// The last 24 hours come in hourly buckets and the longer ranges in days, and until the first response the range decides
	const hourly = $derived(overview ? overview.bucket === 'hour' : range.value === '24h');
	const runsTitle = $derived(hourly ? 'Runs per hour' : 'Runs per day');
	const usageTitle = $derived(`${usage.label} per ${hourly ? 'hour' : 'day'}`);

	const hasRunsInPeriod = $derived(perDay.some((d) => bucketRuns(d) > 0));
	const hasUsageInPeriod = $derived(costByJob.some((c) => usage.pick(c) > 0));

	// A workspace that never ran anything gets a welcome instead of a wall of zeros
	// The period's figures are zero too when every run is older, so only the workspace-wide flag tells the two apart and keeps the period picker for the latter
	const isFreshInstall = $derived(!!overview && !overview.hasRuns);

	const finishedCurrent = $derived(current ? current.succeeded + current.failed : 0);
	const finishedPrevious = $derived(previous ? previous.succeeded + previous.failed : 0);
	// A period's usage of runs and of learning, as a price or in tokens
	function periodUsage(totals: StatsTotals | undefined) {
		if (!totals) return { run: 0, learning: 0, total: 0 };
		const run = usage.pick({ cost: totals.runCost, tokens: totals.runTokens });
		const learning = usage.pick({ cost: totals.learningCost, tokens: totals.learningTokens });
		return { run, learning, total: run + learning };
	}
	const usageCurrent = $derived(periodUsage(current));
	const usagePrevious = $derived(periodUsage(previous));
	// What a run uses on average, and the share of learning when there was any, since the headline already includes it
	const usageFooter = $derived.by(() => {
		if (!current || current.runs === 0) return null;
		const perRun = `${usage.formatBare(usageCurrent.total / current.runs)} per run`;
		if (usageCurrent.learning === 0) return perRun;
		return `${perRun} · ${usage.formatBare(usageCurrent.learning)} learning`;
	});

	// A previous period whose runs used nothing has nothing to compare with, but it has data, so the card shows no comparison at all
	const usageDelta = $derived(
		usagePrevious.total === 0 && (previous?.runs ?? 0) > 0
			? undefined
			: percentDelta(usageCurrent.total, usagePrevious.total, false)
	);

	// Counts get thousands separators, e.g. `1,204`
	function formatCount(n: number) {
		return n.toLocaleString('en-US');
	}

	// Why a run failed, by the run page's rule: when the agent itself reported the failure, its summary says why, while the error only says that it did
	// Other errors are Go error strings in lower case, since they are meant to be wrapped into longer messages, so they get a capital
	function failureReason(run: FailedRun): string | null {
		if (run.status === 'failed' && run.summary && AGENT_FAILURE_PATTERN.test(run.error ?? '')) {
			return run.summary;
		}
		return run.error ? sentenceCase(run.error) : null;
	}

	// Says once which period the figures cover and what their changes compare with, so the cards don't each repeat it
	const description = $derived(
		isFreshInstall
			? undefined
			: `${sentenceCase(shownRange.period)}, compared with the ${shownRange.previous}.`
	);
</script>

<svelte:head>
	<title>Dashboard · Umpteenth</title>
</svelte:head>

<PageHeader title="Dashboard" {description}>
	{#snippet actions()}
		<!-- A fresh workspace has nothing to filter by period -->
		{#if !isFreshInstall}
			<Tabs.Root value={range.value} onValueChange={setRange}>
				<Tabs.List aria-label="Period">
					{#each RANGES as r (r.value)}
						<Tabs.Trigger value={r.value} title={r.label}>
							<span class="numeric">{r.shortLabel}</span>
						</Tabs.Trigger>
					{/each}
				</Tabs.List>
			</Tabs.Root>
		{/if}
	{/snippet}
</PageHeader>

<!-- A run in its list: the job name truncates on its own, so the run number after it always stays visible -->
{#snippet runName(run: { jobName: string; number: number })}
	<span class="flex min-w-0 flex-1 items-baseline gap-1">
		<span class="truncate font-medium" title={run.jobName}>{run.jobName}</span>
		<span class="text-muted-foreground numeric shrink-0">#{run.number}</span>
	</span>
{/snippet}

{#if !overview && !loadError}
	<!-- The skeleton has the shape of the loaded page, so nothing jumps when the figures arrive -->
	<div class="flex flex-col gap-6" aria-busy="true" aria-label="Loading the dashboard">
		<div class="grid grid-cols-2 gap-3 sm:gap-4 xl:grid-cols-4">
			<!-- The labels are known up front, so only the figures wait, and the header is exactly as tall as a loaded card's -->
			{#each kpiLabels as label (label)}
				<Card.Root size="tile">
					<Card.Header>
						<Card.Description variant="label">{label}</Card.Description>
					</Card.Header>
					<Card.Content class="flex flex-col">
						<div class="flex flex-1 flex-col gap-3">
							<Skeleton radius="md" class="h-7.5 w-16 sm:h-8.75" />
							<!-- As tall as the footer row every loaded card reserves -->
							<Skeleton radius="md" class="mt-auto h-4.5 w-28 max-w-full" />
						</div>
					</Card.Content>
				</Card.Root>
			{/each}
		</div>
		<div class="grid gap-4 xl:grid-cols-2">
			{#each [runsTitle, usageTitle] as title (title)}
				<Card.Root>
					<Card.Header>
						<Card.Title>{title}</Card.Title>
						<Skeleton radius="md" class="my-0.5 h-3.5 w-40" />
					</Card.Header>
					<Card.Content>
						<div class="h-chart">
							<Skeleton radius="md" class="h-full" />
						</div>
					</Card.Content>
				</Card.Root>
			{/each}
		</div>
		<div class="grid gap-4 md:grid-cols-2">
			{#each ['Recent failures', usage.trendLabel] as title (title)}
				<Card.Root>
					<Card.Header>
						<Card.Title>{title}</Card.Title>
						<Skeleton radius="md" class="my-0.5 h-3.5 w-48 max-w-full" />
					</Card.Header>
					<!-- Five rows of a name and a detail line, the most a list shows -->
					<Card.Content>
						<div class="flex flex-col gap-4">
							{#each [0, 1, 2, 3, 4] as row (row)}
								<Skeleton radius="md" class="h-8 first:mt-1 last:mb-1" />
							{/each}
						</div>
					</Card.Content>
				</Card.Root>
			{/each}
		</div>
	</div>
{:else if !overview}
	<Empty.Root variant="panel" size="lg" class="flex-none">
		<Empty.Header>
			<Empty.Media variant="icon">
				<TriangleAlertIcon />
			</Empty.Media>
			<Empty.Title>The dashboard could not be loaded</Empty.Title>
			<Empty.Description>{getErrorMessage(loadError)}</Empty.Description>
		</Empty.Header>
		<Empty.Content>
			<Button variant="outline" onclick={retry}>Try again</Button>
		</Empty.Content>
	</Empty.Root>
{:else if isFreshInstall}
	<Empty.Root variant="panel" size="lg" class="flex-none" data-testid="dashboard-empty">
		<Empty.Header>
			<Empty.Media variant="icon">
				<LayoutDashboardIcon />
			</Empty.Media>
			<Empty.Title>Nothing to show yet</Empty.Title>
			<Empty.Description>
				Once jobs start running, their results, durations and {usage.noun} show up here.
			</Empty.Description>
		</Empty.Header>
		<Empty.Content>
			<Button href="/jobs/new">
				<PlusIcon data-icon="inline-start" />
				Create job
			</Button>
		</Empty.Content>
	</Empty.Root>
{:else if current && previous}
	<!-- The figures of the previously picked range dim while the newly picked one loads -->
	<div
		class="flex flex-col gap-6 transition-opacity motion-reduce:transition-none"
		class:opacity-60={pending}
		aria-busy={pending}
	>
		<section class="grid grid-cols-2 gap-3 sm:gap-4 xl:grid-cols-4" aria-label="Key figures">
			<KpiCard
				label="Runs"
				value={formatCount(current.runs)}
				delta={percentDelta(current.runs, previous.runs, null)}
				rangeLabel={shownRange.previous}
			>
				{#snippet footer()}
					<p>{formatCount(current.succeeded)} succeeded · {formatCount(current.failed)} failed</p>
				{/snippet}
			</KpiCard>
			<KpiCard
				label="Success rate"
				value={finishedCurrent > 0 ? `${Math.round(current.successRate * 100)}%` : '—'}
				delta={finishedCurrent === 0
					? undefined
					: finishedPrevious > 0
						? pointsDelta(current.successRate, previous.successRate)
						: null}
				rangeLabel={shownRange.previous}
			>
				{#snippet footer()}
					<div
						class="bg-muted my-1.5 flex h-1.5 overflow-hidden rounded-full"
						style:--success-share="{current.successRate * 100}%"
						aria-hidden="true"
					>
						{#if finishedCurrent > 0}
							<span class="bg-success h-full w-(--success-share)"></span>
							<span class="bg-destructive h-full flex-1"></span>
						{/if}
					</div>
				{/snippet}
			</KpiCard>
			<KpiCard
				label="Median duration"
				value={current.p50Ms > 0 ? formatDuration(current.p50Ms) : '—'}
				delta={current.p50Ms > 0 ? percentDelta(current.p50Ms, previous.p50Ms, false) : undefined}
				rangeLabel={shownRange.previous}
			>
				{#snippet footer()}
					{#if current.p95Ms > 0}
						<p>95% finish within {formatDuration(current.p95Ms)}</p>
					{/if}
				{/snippet}
			</KpiCard>
			<KpiCard
				label={usage.totalLabel}
				value={usage.formatBare(usageCurrent.total)}
				delta={usageDelta}
				rangeLabel={shownRange.previous}
			>
				{#snippet footer()}
					{#if usageFooter}
						<p>{usageFooter}</p>
					{/if}
				{/snippet}
			</KpiCard>
		</section>

		<section class="grid gap-4 xl:grid-cols-2" aria-label="Charts">
			<Card.Root>
				<Card.Header>
					<Card.Title>{runsTitle}</Card.Title>
					<Card.Description>By status</Card.Description>
				</Card.Header>
				<Card.Content>
					{#if hasRunsInPeriod}
						<RunsPerDayChart
							{perDay}
							bucket={overview.bucket}
							bucketMs={overview.bucketMs}
							title={runsTitle}
						/>
					{:else}
						<p class="text-muted-foreground h-chart flex items-center justify-center text-sm">
							No runs in {shownRange.period}
						</p>
					{/if}
				</Card.Content>
			</Card.Root>
			<Card.Root>
				<Card.Header>
					<Card.Title>{usageTitle}</Card.Title>
					<Card.Description>By job, including learning</Card.Description>
				</Card.Header>
				<Card.Content>
					{#if hasUsageInPeriod}
						<CostByJobChart
							{perDay}
							{costByJob}
							bucket={overview.bucket}
							bucketMs={overview.bucketMs}
							title={usageTitle}
							{usage}
						/>
					{:else}
						<p class="text-muted-foreground h-chart flex items-center justify-center text-sm">
							{usage.noneLabel} in {shownRange.period}
						</p>
					{/if}
				</Card.Content>
			</Card.Root>
		</section>

		<!-- Runs in progress get a full-width card on top while there are any, so the two lists below keep the width their names need -->
		<section class="grid gap-4 md:grid-cols-2" aria-label="Runs">
			{#if running.length > 0}
				<Card.Root data-testid="running-now" class="md:col-span-2">
					<Card.Header>
						<Card.Title>Running now</Card.Title>
						<Card.Description>
							{running.length}
							{running.length === 1 ? 'run' : 'runs'} queued or in progress
						</Card.Description>
					</Card.Header>
					<Card.Content>
						<ul class="-mx-2 -my-1 grid grid-cols-1 gap-x-4 md:grid-cols-2 xl:grid-cols-3">
							{#each running as run (run.id)}
								<li>
									<a
										href="/runs/{run.id}"
										class="hover:bg-muted/60 flex items-center gap-3 rounded-lg px-2 py-2 text-sm"
									>
										<StatusBadge status={run.status} iconOnly />
										{@render runName(run)}
										<!-- A queued run has not started, so it shows that instead of a timer that would count its wait -->
										<span class="numeric text-muted-foreground shrink-0 text-xs">
											{run.startedAt
												? formatDuration(Math.max(0, secondClock.now - run.startedAt))
												: 'Queued'}
										</span>
									</a>
								</li>
							{/each}
						</ul>
					</Card.Content>
				</Card.Root>
			{/if}

			<Card.Root data-testid="recent-failures">
				<Card.Header>
					<Card.Title>Recent failures</Card.Title>
					<Card.Description>Latest 5 failed or timed-out runs, from any date</Card.Description>
				</Card.Header>
				<Card.Content>
					{#if recentFailures.length === 0}
						<p class="text-muted-foreground text-sm">No failed runs so far</p>
					{:else}
						<ul class="-mx-2 -my-1 flex flex-col">
							{#each recentFailures as run (run.id)}
								{@const reason = failureReason(run)}
								<li>
									<a
										href="/runs/{run.id}"
										class="hover:bg-muted/60 flex items-start gap-3 rounded-lg px-2 py-2 text-sm"
									>
										<StatusBadge status={run.status} iconOnly />
										<span class="flex min-w-0 flex-1 flex-col">
											<span class="flex min-w-0 items-baseline gap-3">
												{@render runName(run)}
												<RelativeTime
													value={run.startedAt ?? run.queuedAt}
													interactive={false}
													class="text-muted-foreground shrink-0 text-xs"
												/>
											</span>
											{#if reason}
												<span class="text-muted-foreground truncate text-xs" title={reason}>
													{reason}
												</span>
											{/if}
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
					<Card.Title>{usage.trendLabel}</Card.Title>
					<Card.Description>First vs latest 3 successful runs, last 90 days</Card.Description>
				</Card.Header>
				<Card.Content>
					{#if trend.length === 0}
						<p class="text-muted-foreground text-sm">
							Jobs show up here once their recent runs {usage.fallsPhrase} than their first ones
						</p>
					{:else}
						<ul class="-mx-2 -my-1 flex flex-col">
							{#each trend as job (job.jobId)}
								{@const first = usage.pick({ cost: job.firstCost, tokens: job.firstTokens })}
								{@const recent = usage.pick({ cost: job.recentCost, tokens: job.recentTokens })}
								{@const drop = usage.unit === 'tokens' ? job.tokenDropPct : job.dropPct}
								<li>
									<a
										href="/jobs/{job.jobId}"
										class="hover:bg-muted/60 flex items-start gap-3 rounded-lg px-2 py-2 text-sm"
									>
										<span class="flex size-5 shrink-0 items-center justify-center">
											<TrendingDownIcon class="text-success size-4" aria-hidden="true" />
										</span>
										<span class="flex min-w-0 flex-1 flex-col">
											<span class="flex min-w-0 items-baseline gap-3">
												<span class="min-w-0 flex-1 truncate font-medium" title={job.jobName}>
													{job.jobName}
												</span>
												<span class="numeric text-success-foreground shrink-0 text-xs font-medium">
													−{Math.round(drop * 100)}%
												</span>
											</span>
											<span class="text-muted-foreground numeric truncate text-xs">
												{usage.formatBare(first)} → {usage.format(recent)} · {job.runs} runs
											</span>
										</span>
									</a>
								</li>
							{/each}
						</ul>
					{/if}
				</Card.Content>
			</Card.Root>
		</section>
	</div>
{/if}
