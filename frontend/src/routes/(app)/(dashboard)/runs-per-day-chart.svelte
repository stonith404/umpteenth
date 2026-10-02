<script lang="ts" module>
	import type { StatsDayBucket } from '#lib/api/types.js';
	import type { ChartConfig } from '#lib/components/ui/chart/index.js';
	import { NEUTRAL_CHART_COLOR } from './chart-util';

	// Kumo's semantic chart colours, taken from the status tokens so the bars match the badges and the jobs list's run history in both themes
	// The legend and tooltip carry the names, so colour is never the only cue
	const chartConfig = {
		succeeded: { label: 'Succeeded', color: 'var(--success)' },
		failed: { label: 'Failed', color: 'var(--destructive)' },
		timedOut: { label: 'Timed out', color: 'var(--warning)' },
		cancelled: { label: 'Cancelled', theme: NEUTRAL_CHART_COLOR },
		other: { label: 'In progress', color: 'var(--info)' }
	} satisfies ChartConfig;

	type SeriesKey = keyof typeof chartConfig;
	const SERIES_KEYS = Object.keys(chartConfig) as SeriesKey[];

	// The runs a bucket's bar stacks up, which leaves out skipped runs just like the Runs figure above the chart
	export function bucketRuns(bucket: StatsDayBucket): number {
		return SERIES_KEYS.reduce((sum, key) => sum + bucket[key], 0);
	}
</script>

<script lang="ts">
	import type { StatsBucket } from '#lib/api/types.js';
	import BucketBarChart from './bucket-bar-chart.svelte';
	import { formatBucketMoment } from './chart-util';

	let {
		perDay,
		bucket,
		bucketMs,
		title
	}: { perDay: StatsDayBucket[]; bucket: StatsBucket; bucketMs: number; title: string } = $props();

	const rows = $derived(perDay.map((b) => ({ ...b, date: new Date(b.day) })));

	// Statuses that never occur in the period stay out of the legend
	const series = $derived(
		SERIES_KEYS.filter((key) => rows.some((row) => row[key] > 0)).map((key) => ({
			key,
			label: chartConfig[key].label,
			color: `var(--color-${key})`
		}))
	);

	const count = (n: number) => `${n.toLocaleString('en-US')} ${n === 1 ? 'run' : 'runs'}`;

	// A sentence for screen readers, e.g. `Runs per day, Sep 21 to Sep 27: 16 runs, 13 succeeded, 3 failed`
	const label = $derived.by(() => {
		if (rows.length === 0) return title;
		const total = perDay.reduce((sum, b) => sum + bucketRuns(b), 0);
		const parts = series.map((s) => {
			const n = perDay.reduce((sum, b) => sum + b[s.key], 0);
			return `${n.toLocaleString('en-US')} ${s.label.toLowerCase()}`;
		});
		const span = `${formatBucketMoment(rows[0].date, bucket)} to ${formatBucketMoment(rows[rows.length - 1].date, bucket)}`;
		return `${title}, ${span}: ${[count(total), ...parts].join(', ')}`;
	});
</script>

<BucketBarChart
	{rows}
	config={chartConfig}
	{series}
	{bucket}
	{bucketMs}
	integer
	tickFormat={(v) => v.toLocaleString('en-US')}
	valueFormatter={(v) => v.toLocaleString('en-US')}
	total={(items) => count(items.reduce((sum, item) => sum + Number(item.value), 0))}
	emptyLabel="No runs"
	{label}
/>
