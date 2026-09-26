import { formatDate } from '$lib/utils/format-util';
import { CalendarDate, getLocalTimeZone, parseDate } from '@internationalized/date';

// The date range filter of run tables lives in the URL as either a relative preset (`7d`) or two ISO dates (`2026-09-01,2026-09-20`)
// Presets stay relative to now, so a shared "last 24 hours" link means the same thing tomorrow

export const DATE_RANGE_PRESETS = [
	{ value: '24h', label: 'Last 24 hours', ms: 24 * 60 * 60 * 1000 },
	{ value: '7d', label: 'Last 7 days', ms: 7 * 24 * 60 * 60 * 1000 },
	{ value: '30d', label: 'Last 30 days', ms: 30 * 24 * 60 * 60 * 1000 }
] as const;

export type DateRangeBounds = { from?: number; to?: number };

// Converts the URL values to the `from`/`to` unix ms parameters of the runs endpoint, where `to` is exclusive
export function dateRangeToBounds(values: string[], now = Date.now()): DateRangeBounds {
	const [first, second] = values;
	if (!first) return {};

	const preset = DATE_RANGE_PRESETS.find((p) => p.value === first);
	if (preset) return { from: now - preset.ms };

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

	const tz = getLocalTimeZone();
	const startLabel = formatDate(start.toDate(tz).getTime());
	if (start.compare(end) === 0) return startLabel;
	return `${startLabel} – ${formatDate(end.toDate(tz).getTime())}`;
}

export function parseIsoDate(value: string | undefined): CalendarDate | null {
	if (!value) return null;
	try {
		return parseDate(value);
	} catch {
		return null;
	}
}
