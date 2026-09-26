<script lang="ts" module>
	// One bar per bucket, with a value per series key next to the bucket's start
	export type BucketRow = { date: Date; [key: string]: number | Date };
	export type BucketSeries = { key: string; label: string; color: string };
</script>

<script lang="ts">
	import type { StatsBucket } from '$lib/api/types';
	import * as Chart from '$lib/components/ui/chart';
	import type { TooltipPayload } from '$lib/components/ui/chart/chart-utils';
	import { BarChart, type ChartState } from 'layerchart';
	import ChartLegend from './chart-legend.svelte';
	import {
		barPadding,
		bucketTicks,
		CHART_PADDING,
		formatBucketLabel,
		formatBucketTick,
		niceTicks
	} from './chart-util';

	let {
		rows,
		config,
		series,
		bucket,
		bucketMs,
		integer = false,
		tickFormat,
		valueFormatter,
		total,
		emptyLabel,
		label
	}: {
		rows: BucketRow[];
		config: Chart.ChartConfig;
		// The series in legend order, which is also the stack's order from the bottom up
		series: BucketSeries[];
		bucket: StatsBucket;
		bucketMs: number;
		// Counts get whole-number ticks only
		integer?: boolean;
		// Formats a y tick, given the step between ticks so neighbours share their decimals
		tickFormat: (value: number, step: number) => string;
		valueFormatter: (value: number) => string;
		total: (items: TooltipPayload[]) => string;
		emptyLabel: string;
		// The chart's text alternative, a sentence that sums up what it shows
		label: string;
	} = $props();

	// The plot's width decides how many x labels fit and how wide the bars get
	let width = $state(0);
	const plotWidth = $derived(Math.max(0, width - CHART_PADDING.left - CHART_PADDING.right));

	// The y axis ends on a tick just above the tallest stack, so every grid line carries a label
	const max = $derived(
		Math.max(0, ...rows.map((row) => series.reduce((sum, s) => sum + Number(row[s.key] ?? 0), 0)))
	);
	const y = $derived(niceTicks(max, integer));
	const xTicks = $derived(
		bucketTicks(
			rows.map((r) => r.date),
			bucket,
			plotWidth
		)
	);
	const padding = $derived(barPadding(rows.length, plotWidth));

	let chart = $state<ChartState>();
	let plot = $state<HTMLElement>();
	// A touchscreen ends every tap with a pointerleave, which would hide the tooltip as soon as the finger lifts, so a tapped bar's tooltip is locked
	let tooltipLocked = $state(false);

	function onPlotPointerUp(e: PointerEvent) {
		if (e.pointerType === 'touch' && chart?.tooltip.data != null) tooltipLocked = true;
	}

	// The next touch unlocks it before the bars see it, so a tap on another bar moves the tooltip there and a tap anywhere else puts it away
	// A new touch always starts with a pointerover, which reaches the window before the bar under the finger gets its pointerenter
	function onWindowPointerOver(e: PointerEvent) {
		if (!tooltipLocked) return;
		tooltipLocked = false;
		if (!(e.target instanceof Node && plot?.contains(e.target))) chart?.tooltip.hide();
	}
</script>

<svelte:window onpointerover={onWindowPointerOver} />

<!-- The legend sits inside the container, where the series colours are defined, and the plot takes the rest of the chart's fixed height -->
<!-- A legend reserves two rows, so the plot keeps one height side by side with the other chart, and only a phone's third row of job names takes from it -->
<Chart.Container {config} class="h-chart aspect-auto w-full flex-col" role="img" aria-label={label}>
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="min-h-0 w-full flex-1"
		bind:clientWidth={width}
		bind:this={plot}
		onpointerup={onPlotPointerUp}
	>
		<BarChart
			bind:context={chart}
			tooltipContext={{ locked: tooltipLocked }}
			data={rows}
			x="date"
			{series}
			seriesLayout="stack"
			bandPadding={padding}
			stackPadding={1}
			yDomain={[0, y.ticks[y.ticks.length - 1]]}
			yNice={false}
			padding={CHART_PADDING}
			props={{
				bars: { radius: 2, strokeWidth: 0 },
				grid: { yTicks: y.ticks, y: { dashArray: '3 3' } },
				xAxis: { ticks: xTicks, format: (d: Date) => formatBucketTick(d, bucket) },
				yAxis: { ticks: y.ticks, format: (v: number) => tickFormat(v, y.step) }
			}}
		>
			{#snippet tooltip()}
				<Chart.Tooltip
					labelFormatter={(d: Date) => formatBucketLabel(d, bucket, bucketMs)}
					{valueFormatter}
					{total}
					{emptyLabel}
				/>
			{/snippet}
		</BarChart>
	</div>
	<ChartLegend {series} />
</Chart.Container>
