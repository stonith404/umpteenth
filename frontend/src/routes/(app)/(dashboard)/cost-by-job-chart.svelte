<script lang="ts" module>
	import { NEUTRAL_CHART_COLOR } from './chart-util';

	// Kumo's categorical chart palette in its order, with its own dark step for yellow
	// Jobs are categories rather than outcomes, and "Other" takes the neutral of the runs chart
	const JOB_COLORS = [
		{ light: '#4290F0', dark: '#4290F0' },
		{ light: '#F5B647', dark: '#EEB720' },
		{ light: '#E8649D', dark: '#E8649D' },
		{ light: '#8D58EE', dark: '#8D58EE' }
	];
	// The jobs that used the most get their own colour and the rest share "Other", so there are few colours to tell apart and the legend stays short
	const TOP_JOBS = 3;
</script>

<script lang="ts">
	import type { StatsBucket, StatsDayBucket, StatsJobCost } from '#lib/api/types.js';
	import type * as Chart from '#lib/components/ui/chart/index.js';
	import type { UsageFormat } from '#lib/utils/usage-util.js';
	import BucketBarChart from './bucket-bar-chart.svelte';
	import { formatBucketMoment } from './chart-util';

	let {
		perDay,
		costByJob,
		bucket,
		bucketMs,
		title,
		usage
	}: {
		perDay: StatsDayBucket[];
		costByJob: StatsJobCost[];
		bucket: StatsBucket;
		bucketMs: number;
		title: string;
		// The workspace's unit, a price in micro-USD or tokens, which the bars, axis and tooltip all use
		usage: UsageFormat;
	} = $props();

	// Each job's usage over the whole period, the job that used the most first, so the top jobs are picked by the unit on screen
	const jobTotals = $derived.by(() => {
		// Plain Maps are intended in derived values, they are rebuilt on every change and never mutated afterwards
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const totals = new Map<string, { name: string; value: number }>();
		for (const entry of costByJob) {
			const total = totals.get(entry.jobId) ?? { name: entry.jobName, value: 0 };
			total.value += usage.pick(entry);
			totals.set(entry.jobId, total);
		}
		return [...totals.entries()]
			.filter(([, t]) => t.value > 0)
			.sort((a, b) => b[1].value - a[1].value)
			.map(([jobId, t]) => ({ jobId, ...t }));
	});

	// The top jobs get their own series and the rest folds into "Other", unless only one job is left over, which then keeps its name
	const topJobs = $derived(
		(jobTotals.length === TOP_JOBS + 1 ? jobTotals : jobTotals.slice(0, TOP_JOBS)).map(
			(job, i) => ({
				...job,
				key: `job${i}`
			})
		)
	);
	const hasOther = $derived(jobTotals.length > topJobs.length);

	const chartConfig = $derived<Chart.ChartConfig>({
		...Object.fromEntries(
			topJobs.map((job, i) => [job.key, { label: job.name, theme: JOB_COLORS[i] }])
		),
		...(hasOther ? { other: { label: 'Other', theme: NEUTRAL_CHART_COLOR } } : {})
	});

	const series = $derived([
		...topJobs.map((job) => ({ key: job.key, label: job.name, color: `var(--color-${job.key})` })),
		...(hasOther ? [{ key: 'other', label: 'Other', color: 'var(--color-other)' }] : [])
	]);

	// One row per bucket of the period in the workspace's unit, with zero for jobs that used nothing in it
	const rows = $derived.by(() => {
		const keyByJob = new Map(topJobs.map((j) => [j.jobId, j.key]));
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const byBucket = new Map<number, Record<string, number>>();
		for (const b of perDay) {
			byBucket.set(b.day, Object.fromEntries(series.map((s) => [s.key, 0])));
		}
		for (const entry of costByJob) {
			const row = byBucket.get(entry.day);
			if (!row) continue;
			const key = keyByJob.get(entry.jobId) ?? 'other';
			if (key in row) row[key] += usage.pick(entry);
		}
		return [...byBucket.entries()].map(([day, values]) => ({ date: new Date(day), ...values }));
	});

	// A sentence for screen readers, e.g. `Cost per day, Sep 21 to Sep 27: $1.11 in total, most of it from Triage new GitHub issues`
	const label = $derived.by(() => {
		if (rows.length === 0) return title;
		const total = jobTotals.reduce((sum, job) => sum + job.value, 0);
		const span = `${formatBucketMoment(rows[0].date, bucket)} to ${formatBucketMoment(rows[rows.length - 1].date, bucket)}`;
		const top = jobTotals[0] ? `, most of it from ${jobTotals[0].name}` : '';
		return `${title}, ${span}: ${usage.format(total)} in total${top}`;
	});
</script>

<BucketBarChart
	{rows}
	config={chartConfig}
	{series}
	{bucket}
	{bucketMs}
	integer={usage.integer}
	tickFormat={usage.formatTick}
	valueFormatter={usage.formatBare}
	total={(items) => usage.formatBare(items.reduce((sum, item) => sum + Number(item.value), 0))}
	emptyLabel={usage.noneLabel}
	{label}
/>
