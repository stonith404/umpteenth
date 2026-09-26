<script lang="ts" module>
	import type { ChartConfig } from '$lib/components/ui/chart';

	// Status colors (PLAN §13) with their own light and dark steps; the legend and tooltip carry the names, so color is never the only cue
	const chartConfig = {
		succeeded: { label: 'Succeeded', theme: { light: '#16a34a', dark: '#22c55e' } },
		failed: { label: 'Failed', theme: { light: '#dc2626', dark: '#ef4444' } },
		timedOut: { label: 'Timed out', theme: { light: '#ea580c', dark: '#f97316' } },
		cancelled: { label: 'Cancelled', theme: { light: '#a1a1aa', dark: '#71717a' } },
		skipped: { label: 'Skipped', theme: { light: '#d4d4d8', dark: '#52525b' } },
		other: { label: 'In progress', theme: { light: '#2563eb', dark: '#60a5fa' } }
	} satisfies ChartConfig;

	type SeriesKey = keyof typeof chartConfig;
	const SERIES_KEYS = Object.keys(chartConfig) as SeriesKey[];
</script>

<script lang="ts">
	import type { StatsDayBucket } from '$lib/api/types';
	import * as Chart from '$lib/components/ui/chart';
	import { BarChart } from 'layerchart';
	import { formatDay, sparseDayTicks } from './chart-util';

	let { perDay }: { perDay: StatsDayBucket[] } = $props();

	const rows = $derived(perDay.map((bucket) => ({ ...bucket, date: new Date(bucket.day) })));

	// Statuses that never occur in the period stay out of the legend
	const series = $derived(
		SERIES_KEYS.filter((key) => rows.some((row) => row[key] > 0)).map((key) => ({
			key,
			label: chartConfig[key].label,
			color: `var(--color-${key})`
		}))
	);

	const xTicks = $derived(sparseDayTicks(rows.map((r) => r.date)));
</script>

<!-- The legend sits inside the container, where the series colors are defined, below a fixed-height plot -->
<Chart.Container config={chartConfig} class="aspect-auto w-full flex-col">
	<div class="h-60 w-full">
		<BarChart
			data={rows}
			x="date"
			{series}
			seriesLayout="stack"
			bandPadding={0.3}
			props={{
				bars: { radius: 2, class: 'stroke-card stroke-1' },
				xAxis: { ticks: xTicks, format: formatDay },
				yAxis: { format: (v: number) => (Number.isInteger(v) ? String(v) : '') }
			}}
		>
			{#snippet tooltip()}
				<Chart.Tooltip labelFormatter={(d: Date) => formatDay(d)} />
			{/snippet}
		</BarChart>
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
