import { formatShortDate } from '#lib/utils/format-util.js';
import { CalendarDate, getLocalTimeZone, parseDate } from '@internationalized/date';

// The date range filter of run tables lives in the URL as either a relative preset (`7d`) or two ISO dates (`2026-09-01,2026-09-20`)
// Presets stay relative to now, so a shared "last 24 hours" link means the same thing tomorrow

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

// The one list of relative time ranges, so every picker names them the same way
// Segmented controls use `shortLabel` ("7d"), menus use `label` ("Last 7 days"), and sentences use `period` ("the last 7 days") and `previous` ("previous 7 days")
export const TIME_RANGES = [
	{
		value: '24h',
		shortLabel: '24h',
		label: 'Last 24 hours',
		period: 'the last 24 hours',
		previous: 'previous 24 hours',
		ms: 24 * HOUR_MS
	},
	{
		value: '7d',
		shortLabel: '7d',
		label: 'Last 7 days',
		period: 'the last 7 days',
		previous: 'previous 7 days',
		ms: 7 * DAY_MS
	},
	{
		value: '30d',
		shortLabel: '30d',
		label: 'Last 30 days',
		period: 'the last 30 days',
		previous: 'previous 30 days',
		ms: 30 * DAY_MS
	},
	{
		value: '90d',
		shortLabel: '90d',
		label: 'Last 90 days',
		period: 'the last 90 days',
		previous: 'previous 90 days',
		ms: 90 * DAY_MS
	}
] as const;

export type TimeRange = (typeof TIME_RANGES)[number];
export type TimeRangeValue = TimeRange['value'];

// Picks the ranges a control offers, in the canonical order, e.g. `timeRanges('7d', '30d', '90d')`
export function timeRanges<V extends TimeRangeValue>(
	...values: V[]
): Extract<TimeRange, { value: V }>[] {
	return TIME_RANGES.filter((range): range is Extract<TimeRange, { value: V }> =>
		values.includes(range.value as V)
	);
}

// The presets of the run tables' date filter
export const DATE_RANGE_PRESETS = TIME_RANGES;

export type DateRangeBounds = { from?: number; to?: number };

// Converts the URL values to the `from`/`to` unix ms parameters of the runs endpoint, where `to` is exclusive
export function dateRangeToBounds(values: string[]): DateRangeBounds {
	const [first, second] = values;
	if (!first) return {};

	const preset = DATE_RANGE_PRESETS.find((p) => p.value === first);
	if (preset) return { from: Date.now() - preset.ms };

	const start = parseIsoDate(first);
	const end = parseIsoDate(second ?? first);
	if (!start || !end) return {};

	// The end date is inclusive in the picker, so the bound is the start of the following day
	const tz = getLocalTimeZone();
	return { from: start.toDate(tz).getTime(), to: end.add({ days: 1 }).toDate(tz).getTime() };
}

// A short label of the selected range for the filter button, or null when no range is set
export function dateRangeLabel(values: string[]): string | null {
	const [first, second] = values;
	if (!first) return null;

	const preset = DATE_RANGE_PRESETS.find((p) => p.value === first);
	if (preset) return preset.label;

	const start = parseIsoDate(first);
	const end = parseIsoDate(second ?? first);
	if (!start || !end) return null;

	// Short dates keep the filter button compact, e.g. `Sep 1 – Sep 20`, with the year only outside the current one
	const tz = getLocalTimeZone();
	const startLabel = formatShortDate(start.toDate(tz).getTime());
	if (start.compare(end) === 0) return startLabel;
	return `${startLabel} – ${formatShortDate(end.toDate(tz).getTime())}`;
}

export function parseIsoDate(value: string | undefined): CalendarDate | null {
	if (!value) return null;
	try {
		return parseDate(value);
	} catch {
		return null;
	}
}
