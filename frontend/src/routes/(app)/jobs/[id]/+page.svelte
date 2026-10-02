<script lang="ts">
	import { goto } from '$app/navigation';
	import { navigating, page } from '$app/state';
	import RunNowDialog from '#lib/components/jobs/run-now-dialog.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Card from '#lib/components/ui/card/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	import { formatDuration, formatRelative, formatShortDate } from '#lib/utils/format-util.js';
	import { cn } from '#lib/utils/style.js';
	import { usageFormat } from '#lib/utils/usage-util.js';
	import ChartColumnIcon from '@lucide/svelte/icons/chart-column';
	import PlayIcon from '@lucide/svelte/icons/play';
	import GraduationChart from './graduation-chart.svelte';
	import { DEFAULT_STATS_RANGE, statsRanges } from './stats-ranges';

	let { data } = $props();

	const job = $derived(data.job);
	const stats = $derived(data.stats);
	const runs = $derived(stats.runs ?? []);

	// The tiles count every run of the period, since the chart's points stop at its newest runs
	const runCount = $derived(stats.runCount);
	const succeeded = $derived(stats.succeeded);
	const failed = $derived(stats.failed);
	const longest = $derived(stats.maxMs);

	// Usage shows as a price or in tokens, as the workspace chose
	const usage = $derived(usageFormat());

	// A tile's context line is a list of phrases, so a narrow tile wraps between them rather than inside one
	const tiles = $derived([
		{
			label: 'Runs',
			value: String(runCount),
			detail: runCount > 0 ? [`${succeeded} succeeded`, `${failed} failed`] : null
		},
		{
			label: 'Success rate',
			value: succeeded + failed > 0 ? `${Math.round(stats.successRate * 100)}%` : '—',
			detail:
				succeeded + failed > 0
					? [`of ${succeeded + failed} finished ${succeeded + failed === 1 ? 'run' : 'runs'}`]
					: null
		},
		{
			label: usage.averageLabel,
			value:
				runCount > 0
					? usage.formatBare(usage.pick({ cost: stats.avgCost, tokens: stats.avgTokens }))
					: '—',
			detail: runCount > 0 ? ['per run, including learning'] : null
		},
		{
			label: 'Median duration',
			value: stats.p50Ms > 0 ? formatDuration(stats.p50Ms) : '—',
			detail: longest > 0 ? [`Longest ${formatDuration(longest)}`] : null
		}
	]);

	// The stats reload through a navigation, so the tiles and chart dim until the new period's numbers arrive
	const pending = $derived(navigating.to?.url.pathname === page.url.pathname);

	// A job that ran before the period says when, in the header's short date, and a longer period is only suggested while there is one
	const emptyPeriodText = $derived.by(() => {
		const lastRan = job.lastRun ? `It last ran on ${formatShortDate(job.lastRun.queuedAt)}.` : '';
		const longer = statsRanges.findIndex((r) => r.value === data.range) < statsRanges.length - 1;
		return longer ? `${lastRan} Pick a longer period to see earlier runs.`.trim() : lastRan;
	});

	let runNowOpen = $state(false);

	function setRange(range: string) {
		const url = new URL(page.url.href);
		if (range === DEFAULT_STATS_RANGE) url.searchParams.delete('range');
		else url.searchParams.set('range', range);
		void goto(url, { reset: false, replace: true });
	}
</script>

<svelte:head>
	<title>Overview · {job.name} · Umpteenth</title>
</svelte:head>

<section class="flex flex-col gap-4" aria-labelledby="performance-heading">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<h2 id="performance-heading" class="text-lg font-semibold">Performance</h2>
		{#if job.lastRun}
			<Tabs.Root value={data.range} onValueChange={setRange}>
				<Tabs.List aria-label="Period">
					{#each statsRanges as range (range.value)}
						<Tabs.Trigger value={range.value} title={range.label}>{range.shortLabel}</Tabs.Trigger>
					{/each}
				</Tabs.List>
			</Tabs.Root>
		{/if}
	</div>

	{#if !job.lastRun}
		<!-- A job that never ran has nothing to measure yet, so the page points at the first run instead of empty tiles -->
		<Empty.Root variant="panel">
			<Empty.Header>
				<Empty.Media variant="icon">
					<ChartColumnIcon />
				</Empty.Media>
				<Empty.Title>No runs yet</Empty.Title>
				<Empty.Description>
					{#if job.cron && job.nextRunAt}
						Its first scheduled run is {formatRelative(job.nextRunAt)}. Runs show up here with their
						{usage.noun}, duration and mode.
					{:else}
						This job runs on demand. Runs show up here with their {usage.noun}, duration and mode.
					{/if}
				</Empty.Description>
			</Empty.Header>
			<Empty.Content>
				<!-- Outlined, since the header's Run now is already the page's primary action -->
				<Button variant="outline" onclick={() => (runNowOpen = true)}>
					<PlayIcon data-icon="inline-start" />
					Run now
				</Button>
			</Empty.Content>
		</Empty.Root>
	{:else}
		<div
			class="flex flex-col gap-4 transition-opacity motion-reduce:transition-none"
			class:opacity-60={pending}
			aria-busy={pending}
		>
			<div class="grid grid-cols-2 gap-3 sm:gap-4 xl:grid-cols-4">
				{#each tiles as tile (tile.label)}
					<!-- The dashboard's tile: the label in the card's strip and the value on its panel, with a small card's gutters on phones -->
					<Card.Root size="tile">
						<Card.Header>
							<Card.Description variant="label">{tile.label}</Card.Description>
						</Card.Header>
						<Card.Content class="flex flex-col">
							<div class="flex flex-1 flex-col gap-3 text-sm">
								<p class="numeric sm:text-figure text-2xl leading-tight font-semibold">
									{tile.value}
								</p>
								{#if tile.detail}
									<p
										class="text-muted-foreground numeric mt-auto flex flex-wrap gap-x-1 text-xs sm:text-sm"
									>
										{#each tile.detail as phrase, i (i)}
											<span class={cn(tile.detail.length > 1 && 'whitespace-nowrap')}
												>{#if i > 0}<span class="mr-1">·</span>{/if}{phrase}</span
											>
										{/each}
									</p>
								{/if}
							</div>
						</Card.Content>
					</Card.Root>
				{/each}
			</div>

			<Card.Root>
				<Card.Header>
					<Card.Title level={3}>Graduation</Card.Title>
					<Card.Description>
						{usage.label} and duration of every run. As the playbook grows, runs move from Explore to
						Assisted to Scripted and {usage.fallsPhrase}.
					</Card.Description>
				</Card.Header>
				<Card.Content>
					{#if runs.length > 0}
						<GraduationChart {runs} versions={stats.versions ?? []} {usage} />
					{:else}
						<Empty.Root size="md">
							<Empty.Header>
								<Empty.Media variant="icon">
									<ChartColumnIcon />
								</Empty.Media>
								<Empty.Title>No runs in this period</Empty.Title>
								<Empty.Description>{emptyPeriodText}</Empty.Description>
							</Empty.Header>
						</Empty.Root>
					{/if}
				</Card.Content>
			</Card.Root>
		</div>
	{/if}
</section>

<RunNowDialog bind:open={runNowOpen} {job} />
