import type { StatsBucket } from '$lib/api/types';
import { formatShortDate, formatTime } from '$lib/utils/format-util';

// Both charts share one plot frame, so side by side their baselines, top lines and left edges line up
// The left gutter fits the widest y tick label either chart draws, e.g. `$0.60` or `1,000`
export const CHART_PADDING = { left: 44, right: 20, top: 8, bottom: 24 };

// The neutral of cancelled runs and of the "Other" jobs, the same mix of the muted text token the jobs list's run history uses, made opaque so grid lines don't show through
export const NEUTRAL_CHART_COLOR = {
	light: 'color-mix(in oklab, var(--muted-foreground) 40%, var(--card))',
	dark: 'color-mix(in oklab, var(--muted-foreground) 55%, var(--card))'
};

// Day buckets are UTC days, so their labels are formatted in UTC to stay on the right day in every time zone
const dayFormat = new Intl.DateTimeFormat('en-US', {
	month: 'short',
	day: 'numeric',
	timeZone: 'UTC'
});
const weekdayFormat = new Intl.DateTimeFormat('en-US', {
	weekday: 'short',
	month: 'short',
	day: 'numeric',
	timeZone: 'UTC'
});

// An axis label of a bucket: the UTC day, e.g. `Sep 21`, or the local hour, e.g. `14:00`
export function formatBucketTick(value: Date | number, bucket: StatsBucket): string {
	const ms = +value;
	return bucket === 'hour' ? formatTime(ms) : dayFormat.format(ms);
}

// A tooltip heading of a bucket: `Sun, Sep 21`, or `Sep 27, 14:00–15:00` for an hour in the viewer's zone
export function formatBucketLabel(
	value: Date | number,
	bucket: StatsBucket,
	bucketMs: number
): string {
	const ms = +value;
	if (bucket === 'day') return weekdayFormat.format(ms);
	return `${formatShortDate(ms)}, ${formatTime(ms)}–${formatTime(ms + bucketMs)}`;
}

// A bucket as it reads in a sentence, e.g. `Sep 21` or `Sep 27, 14:00`, for the charts' text alternatives
export function formatBucketMoment(value: Date | number, bucket: StatsBucket): string {
	const ms = +value;
	return bucket === 'day' ? dayFormat.format(ms) : `${formatShortDate(ms)}, ${formatTime(ms)}`;
}

// The room one x label needs including the gap to its neighbour, a `Sep 21` is wider than a `14:00`
const TICK_SPACING = { day: 50, hour: 46 };
// Half a label's width, which has to fit between a label's centre and the edge of the chart
const TICK_HALF_WIDTH = { day: 20, hour: 17 };
// Label every n-th bucket with an n that reads naturally: whole hours of the clock, and days in round numbers or whole weeks
const TICK_STEPS = { day: [1, 2, 3, 5, 7, 10, 14, 21, 30], hour: [1, 2, 3, 4, 6, 12] };

// Picks the buckets that get an x label, as many as the plot's width fits without labels touching
// The ticks are the data's own dates, because a chart's automatic time ticks fall on local midnights and would be labelled with the wrong UTC day
// Days count back from the last bucket, which keeps today labelled, and hours fall on round hours of the viewer's clock
export function bucketTicks(dates: Date[], bucket: StatsBucket, plotWidth: number): Date[] {
	const count = dates.length;
	if (count === 0 || plotWidth <= 0) return [];

	// The smallest step whose labels fit the width
	const maxLabels = Math.max(2, Math.floor(plotWidth / TICK_SPACING[bucket]));
	const steps = TICK_STEPS[bucket];
	const step = steps.find((s) => Math.ceil(count / s) <= maxLabels) ?? steps[steps.length - 1];

	// Labels whose centre sits too close to the plot's left edge would run into the y axis, the right edge has the padding to spare
	const band = plotWidth / count;
	const half = TICK_HALF_WIDTH[bucket];
	const fits = (i: number) => (i + 0.5) * band >= half - 4;

	const last = count - 1;
	return dates.filter((date, i) => {
		if (!fits(i)) return false;
		if (bucket === 'hour') return date.getHours() % step === 0;
		return (last - i) % step === 0;
	});
}

// Nice y axis ticks from zero up to at least `max`, at most five intervals, e.g. 0, 2, 4, 6, 8, 10
// Counts step by whole numbers only, so a chart of one or two runs never draws unlabelled half-run lines
// Steps are 1, 2 or 5 times a power of ten, since a 2.5 step needs one more decimal than the cost axis shows and would label $0.075 as $0.08
export function niceTicks(max: number, integer: boolean): { ticks: number[]; step: number } {
	if (!(max > 0)) return { ticks: [0, 1], step: 1 };

	const MAX_INTERVALS = 5;
	const magnitude = 10 ** Math.floor(Math.log10(max / MAX_INTERVALS));
	const candidates = [1, 2, 5, 10]
		.map((m) => m * magnitude)
		.filter((s) => !integer || (s >= 1 && Number.isInteger(s)));
	const step = Math.max(
		integer ? 1 : 0,
		candidates.find((s) => Math.ceil(max / s - 1e-9) <= MAX_INTERVALS) ?? 10 * magnitude
	);

	// Rounded to the step's decimals, so float drift never turns 0.3 into 0.30000000000000004
	const decimals = Math.max(0, -Math.floor(Math.log10(step)) + 1);
	const intervals = Math.max(1, Math.ceil(max / step - 1e-9));
	const ticks = Array.from({ length: intervals + 1 }, (_, i) =>
		Number((i * step).toFixed(decimals))
	);
	return { ticks, step };
}

// Gap between bars as a share of each bucket, wide enough that a week of daily bars doesn't turn into thick blocks
export function barPadding(count: number, plotWidth: number): number {
	const MAX_BAR_WIDTH = 36;
	if (count === 0 || plotWidth <= 0) return 0.3;
	const band = plotWidth / count;
	return Math.min(0.7, Math.max(0.2, 1 - MAX_BAR_WIDTH / band));
}
