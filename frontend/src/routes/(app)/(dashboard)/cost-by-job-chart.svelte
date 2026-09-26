<script lang="ts" module>
	// Categorical slots in fixed order with separate light and dark steps, validated for color-vision deficiencies on adjacent pairs
	const JOB_COLORS = [
		{ light: '#2a78d6', dark: '#3987e5' },
		{ light: '#eb6834', dark: '#d95926' },
		{ light: '#1baf7a', dark: '#199e70' },
		{ light: '#eda100', dark: '#c98500' },
		{ light: '#e87ba4', dark: '#d55181' }
	];
	const OTHER_COLOR = { light: '#a1a1aa', dark: '#71717a' };
	const TOP_JOBS = JOB_COLORS.length;
</script>

<script lang="ts">
	import type { StatsDayBucket, StatsJobCost } from '$lib/api/types';
	import * as Chart from '$lib/components/ui/chart';
	import { formatCost } from '$lib/utils/format-util';
	import { AreaChart } from 'layerchart';
	import { formatDay, sparseDayTicks } from './chart-util';

	let { perDay, costByJob }: { perDay: StatsDayBucket[]; costByJob: StatsJobCost[] } = $props();

	// The most expensive jobs of the period get their own series, the rest folds into "Other"
	const topJobs = $derived.by(() => {
		// Plain Maps are intended in derived values, they are rebuilt on every change and never mutated afterwards
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const totals = new Map<string, { name: string; cost: number }>();
		for (const entry of costByJob) {
			const total = totals.get(entry.jobId) ?? { name: entry.jobName, cost: 0 };
			total.cost += entry.cost;
			totals.set(entry.jobId, total);
		}
		return [...totals.entries()]
			.filter(([, t]) => t.cost > 0)
			.sort((a, b) => b[1].cost - a[1].cost)
			.slice(0, TOP_JOBS)
			.map(([jobId, t], i) => ({ jobId, name: t.name, key: `job${i}` }));
	});
	const hasOther = $derived(
		costByJob.some((c) => c.cost > 0 && !topJobs.some((j) => j.jobId === c.jobId))
	);

	const chartConfig = $derived<Chart.ChartConfig>({
		...Object.fromEntries(
			topJobs.map((job, i) => [job.key, { label: job.name, theme: JOB_COLORS[i] }])
		),
		...(hasOther ? { other: { label: 'Other', theme: OTHER_COLOR } } : {})
	});

	const series = $derived([
		...topJobs.map((job) => ({ key: job.key, label: job.name, color: `var(--color-${job.key})` })),
		...(hasOther ? [{ key: 'other', label: 'Other', color: 'var(--color-other)' }] : [])
	]);

	// One row per day of the period in USD, with zero for jobs that didn't run that day
	const rows = $derived.by(() => {
		const keyByJob = new Map(topJobs.map((j) => [j.jobId, j.key]));
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const byDay = new Map<number, Record<string, number>>();
		for (const bucket of perDay) {
			byDay.set(bucket.day, Object.fromEntries(series.map((s) => [s.key, 0])));
		}
		for (const entry of costByJob) {
			const row = byDay.get(entry.day);
			if (!row) continue;
			const key = keyByJob.get(entry.jobId) ?? 'other';
			if (key in row) row[key] += entry.cost / 1_000_000;
		}
		return [...byDay.entries()].map(([day, values]) => ({ date: new Date(day), ...values }));
	});

	const xTicks = $derived(sparseDayTicks(rows.map((r) => r.date)));
</script>

<!-- The legend sits inside the container, where the series colors are defined, below a fixed-height plot -->
<Chart.Container config={chartConfig} class="aspect-auto w-full flex-col">
	<div class="h-60 w-full">
		<AreaChart
			data={rows}
			x="date"
			{series}
			seriesLayout="stack"
			padding={{ left: 52, bottom: 24, top: 8, right: 12 }}
			props={{
				area: { 'fill-opacity': 0.35, line: { class: 'stroke-2' } },
				xAxis: { ticks: xTicks, format: formatDay },
				yAxis: { format: (v: number) => formatCost(v) }
			}}
		>
			{#snippet tooltip()}
				<Chart.Tooltip labelFormatter={(d: Date) => formatDay(d)}>
					{#snippet formatter({ value, name, item })}
						<div class="flex w-full items-center gap-2">
							<span class="size-2.5 shrink-0 rounded-[2px]" style:background={item.color}></span>
							<span class="text-muted-foreground">{name}</span>
							<span class="numeric text-foreground ml-auto pl-2 font-medium">
								{formatCost(Number(value))}
							</span>
						</div>
					{/snippet}
				</Chart.Tooltip>
			{/snippet}
		</AreaChart>
	</div>
	<!-- A plain HTML legend wraps long job names instead of overlapping the axis -->
	<ul class="mt-3 flex flex-wrap justify-center gap-x-4 gap-y-1 text-xs" aria-label="Legend">
		{#each series as s (s.key)}
			<li class="text-muted-foreground flex items-center gap-1.5">
				<span class="size-2.5 shrink-0 rounded-[2px]" style:background={s.color}></span>
				{s.label}
			</li>
		{/each}
	</ul>
</Chart.Container>
