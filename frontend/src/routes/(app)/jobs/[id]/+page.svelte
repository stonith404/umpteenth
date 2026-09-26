<script lang="ts">
	import { goto } from '$app/navigation';
	import { navigating, page } from '$app/state';
	import RunNowDialog from '$lib/components/jobs/run-now-dialog.svelte';
	import Markdown from '$lib/components/markdown.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Empty from '$lib/components/ui/empty';
	import * as Tabs from '$lib/components/ui/tabs';
	import { formatDuration, formatRelative, formatShortDate } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import { usageFormat } from '$lib/utils/usage-util';
	import ChartColumnIcon from '@lucide/svelte/icons/chart-column';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlayIcon from '@lucide/svelte/icons/play';
	import GraduationChart from './graduation-chart.svelte';
	import { DEFAULT_STATS_RANGE, statsRanges } from './stats-ranges';

	let { data } = $props();

	const job = $derived(data.job);
	const stats = $derived(data.stats);
	const runs = $derived(stats.runs ?? []);
	const spec = $derived(job.spec);

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

	// What each concurrency policy does when a trigger arrives while a run is still active
	const concurrencyText: Record<string, string> = {
		skip: 'Skip the new run',
		queue: 'Start the new run after it',
		parallel: 'Start the new run alongside it'
	};

	const networkText = $derived.by(() => {
		if (job.network === 'none') return 'None';
		if (job.network === 'unrestricted') return 'Unrestricted';
		if (job.network !== 'allowlist') return 'Internet';
		const domains = job.allowedDomains ?? [];
		if (domains.length === 0) return 'No domains allowed';
		return domains.length <= 3 ? domains.join(', ') : `${domains.length} allowed domains`;
	});

	let runNowOpen = $state(false);

	function setRange(range: string) {
		const url = new URL(page.url);
		if (range === DEFAULT_STATS_RANGE) url.searchParams.delete('range');
		else url.searchParams.set('range', range);
		void goto(url, { noScroll: true, keepFocus: true, replaceState: true });
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
		<Empty.Root variant="panel" class="py-12">
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
					<Card.Root>
						<Card.Header class="py-2.5 max-sm:px-3">
							<Card.Description class="truncate text-sm sm:text-base">{tile.label}</Card.Description
							>
						</Card.Header>
						<Card.Content class="flex flex-1 flex-col gap-3 text-sm max-sm:p-3">
							<p class="numeric text-2xl leading-tight font-semibold sm:text-[1.75rem]">
								{tile.value}
							</p>
							{#if tile.detail}
								<p
									class="text-muted-foreground numeric mt-auto flex flex-wrap gap-x-1 text-xs sm:text-sm"
								>
									{#each tile.detail as phrase, i (i)}
										<span
											class={cn(
												tile.detail.length > 1 && 'whitespace-nowrap',
												i > 0 && "before:mr-1 before:content-['·']"
											)}>{phrase}</span
										>
									{/each}
								</p>
							{/if}
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
						<Empty.Root class="py-10">
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

<!-- Side by side, both headers hold only their title and pencil, so the panels below them start at the same height -->
<div class="grid items-start gap-6 lg:grid-cols-2">
	<Card.Root>
		<Card.Header>
			<Card.Title>Instruction</Card.Title>
			<!-- The pencil hangs into the header's padding and takes a single row, so both headers stay as tall as a title -->
			<Card.Action class="row-span-1 -my-2 self-center">
				<Button
					variant="ghost"
					size="icon-sm"
					href="/jobs/{job.id}/settings"
					aria-label="Edit instruction"
				>
					<PencilIcon />
				</Button>
			</Card.Action>
		</Card.Header>
		<Card.Content>
			<Markdown source={job.instruction} />
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title>Spec</Card.Title>
			<Card.Action class="row-span-1 -my-2 self-center">
				<Button
					variant="ghost"
					size="icon-sm"
					href="/jobs/{job.id}/settings"
					aria-label="Edit spec"
				>
					<PencilIcon />
				</Button>
			</Card.Action>
		</Card.Header>
		<Card.Content class="flex flex-col gap-5 text-sm">
			{#if spec.goal}
				<section class="flex flex-col gap-2">
					<h3 class="text-muted-foreground text-xs font-medium">Goal</h3>
					<p>{spec.goal}</p>
				</section>
			{/if}
			{#if spec.successCriteria && spec.successCriteria.length > 0}
				<section class="flex flex-col gap-2">
					<h3 class="text-muted-foreground text-xs font-medium">Success criteria</h3>
					<ul class="flex list-disc flex-col gap-1 pl-4">
						{#each spec.successCriteria as criterion, i (i)}
							<li>{criterion}</li>
						{/each}
					</ul>
				</section>
			{/if}
			{#each [{ title: 'Inputs', fields: spec.inputs ?? [] }, { title: 'Outputs', fields: spec.outputs ?? [] }] as group (group.title)}
				{#if group.fields.length > 0}
					<section class="flex flex-col gap-2">
						<h3 class="text-muted-foreground text-xs font-medium">{group.title}</h3>
						<!-- Fields are keyed by position, since an older job may repeat a name and a repeated key would break the page -->
						<ul class="flex flex-col gap-1.5">
							{#each group.fields as field, i (i)}
								<li class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
									<span class="font-mono text-xs">{field.name}</span>
									<span class="text-muted-foreground font-mono text-xs">{field.type}</span>
									{#if field.description}
										<span class="text-muted-foreground">{field.description}</span>
									{/if}
								</li>
							{/each}
						</ul>
					</section>
				{/if}
			{/each}
			{#if spec.mcp && spec.mcp.length > 0}
				<section class="flex flex-col gap-2">
					<h3 class="text-muted-foreground text-xs font-medium">Services</h3>
					<ul class="flex flex-col gap-1.5">
						{#each spec.mcp as need, i (i)}
							<li class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
								<span class="font-mono text-xs">{need.server}</span>
								<span class="text-muted-foreground">{need.why}</span>
							</li>
						{/each}
					</ul>
				</section>
			{/if}
			{#if spec.sideEffects && spec.sideEffects.length > 0}
				<section class="flex flex-col gap-2">
					<h3 class="text-muted-foreground text-xs font-medium">Side effects</h3>
					<div class="flex flex-wrap gap-1.5">
						{#each spec.sideEffects as effect, i (i)}
							<Badge variant="secondary">{effect}</Badge>
						{/each}
					</div>
				</section>
			{/if}
			<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
				<dt class="text-muted-foreground">Network access</dt>
				<dd class="min-w-0 break-words">{networkText}</dd>
				<dt class="text-muted-foreground">If a run is still active</dt>
				<dd>{concurrencyText[job.concurrency] ?? job.concurrency}</dd>
				<dt class="text-muted-foreground">Learns from runs</dt>
				<dd>{job.selfImprove ? 'On' : 'Off'}</dd>
			</dl>
		</Card.Content>
	</Card.Root>
</div>

<RunNowDialog bind:open={runNowOpen} {job} />
