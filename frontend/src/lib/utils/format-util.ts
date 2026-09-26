// Formatting helpers for the visual language in PLAN §13
// Timestamps from the API are unix milliseconds

// English month names everywhere, with a 24-hour clock so timestamps read like the schedules ("Daily at 07:00")
const LOCALE = 'en-US';
const HOUR_CYCLE = 'h23';

const DAY_MS = 24 * 60 * 60 * 1000;

// Relative wording stays precise for about a week, older and later times read better as a date
const RELATIVE_LIMIT_DAYS = 6;

const dateTimeFormat = new Intl.DateTimeFormat(LOCALE, {
	dateStyle: 'medium',
	timeStyle: 'short',
	hourCycle: HOUR_CYCLE
});
const dateFormat = new Intl.DateTimeFormat(LOCALE, { dateStyle: 'medium' });
const shortDateFormat = new Intl.DateTimeFormat(LOCALE, { month: 'short', day: 'numeric' });
const timeFormat = new Intl.DateTimeFormat(LOCALE, {
	hour: '2-digit',
	minute: '2-digit',
	hourCycle: HOUR_CYCLE
});
const relativeFormat = new Intl.RelativeTimeFormat(LOCALE, { numeric: 'auto' });

// en-US only knows abbreviations for American zones and writes "GMT+2" for Zurich, where en-GB knows "CEST"
const zoneNameFormats = [LOCALE, 'en-GB'].map(
	(locale) => new Intl.DateTimeFormat(locale, { hour: 'numeric', timeZoneName: 'short' })
);

// Each unit covers the values below the next unit's size, so 59.6 minutes reads `1 hour ago` rather than `60 minutes ago`
const RELATIVE_UNITS: [Intl.RelativeTimeFormatUnit, number, number][] = [
	['second', 1000, 60],
	['minute', 60 * 1000, 60],
	['hour', 60 * 60 * 1000, 24],
	['day', DAY_MS, Infinity]
];

type DateTimeOptions = {
	// Appends the viewer's short zone name, e.g. `CEST`, which exact-time tooltips need since the time is in the viewer's zone
	zone?: boolean;
};

// Formats a timestamp as e.g. `Sep 25, 2026, 21:06 CEST`, or without the zone when `zone` is false
export function formatDateTime(ms: number, { zone = true }: DateTimeOptions = {}): string {
	const text = dateTimeFormat.format(ms);
	return zone ? `${text} ${formatZoneName(ms)}` : text;
}

// Formats a timestamp as e.g. `Sep 25, 2026`
export function formatDate(ms: number): string {
	return dateFormat.format(ms);
}

// Formats a timestamp as a compact date, e.g. `Sep 12`, adding the year only outside the current one, e.g. `Sep 12, 2025`
export function formatShortDate(ms: number, now = Date.now()): string {
	const sameYear = new Date(ms).getFullYear() === new Date(now).getFullYear();
	return sameYear ? shortDateFormat.format(ms) : dateFormat.format(ms);
}

// Formats the time of day of a timestamp as e.g. `07:05`
export function formatTime(ms: number): string {
	return timeFormat.format(ms);
}

// The viewer's short zone name at a given moment, e.g. `CEST`, `EDT`, `UTC` or `GMT+9` where no abbreviation exists
export function formatZoneName(ms: number): string {
	let fallback = '';
	for (const format of zoneNameFormats) {
		const name = format.formatToParts(ms).find((part) => part.type === 'timeZoneName')?.value;
		if (!name) continue;
		if (!/^GMT[+-]/.test(name)) return name;
		fallback ||= name;
	}
	return fallback;
}

// Formats a timestamp relative to `now`, e.g. `3 minutes ago`, `yesterday` or `in 4 days`
// Beyond six days it switches to a short date, e.g. `Sep 12`, since "3 weeks ago" is vague and the exact time is in the tooltip
export function formatRelative(ms: number, now = Date.now()): string {
	const diff = ms - now;
	if (Math.abs(diff) < 10_000) return 'just now';
	if (Math.round(Math.abs(diff) / DAY_MS) > RELATIVE_LIMIT_DAYS) return formatShortDate(ms, now);

	for (const [unit, unitMs, limit] of RELATIVE_UNITS) {
		const value = Math.round(diff / unitMs);
		if (Math.abs(value) < limit) return relativeFormat.format(value, unit);
	}
	return formatShortDate(ms, now);
}

// Formats a duration as e.g. `1m 12s`, `850ms` or `2h 5m`
export function formatDuration(ms: number): string {
	if (ms < 1000) return `${Math.round(ms)}ms`;

	const totalSeconds = Math.round(ms / 1000);
	const hours = Math.floor(totalSeconds / 3600);
	const minutes = Math.floor((totalSeconds % 3600) / 60);
	const seconds = totalSeconds % 60;

	if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
	if (minutes > 0) return seconds > 0 ? `${minutes}m ${seconds}s` : `${minutes}m`;
	return `${seconds}s`;
}

const currencyFormats = new Map<string, Intl.NumberFormat>();

// Formats dollars with between `minDigits` and `maxDigits` decimals, caching one formatter per precision
function currency(usd: number, minDigits: number, maxDigits = minDigits) {
	const key = `${minDigits}-${maxDigits}`;
	let format = currencyFormats.get(key);
	if (!format) {
		format = new Intl.NumberFormat(LOCALE, {
			style: 'currency',
			currency: 'USD',
			minimumFractionDigits: minDigits,
			maximumFractionDigits: maxDigits
		});
		currencyFormats.set(key, format);
	}
	return format.format(usd);
}

// Formats a USD amount with cents, e.g. `$0.14`, `$1.11` or `$0.00`
// LLM calls often cost less than a cent, so those amounts keep a third decimal (`$0.004`) and the tiniest read `<$0.001` rather than a misleading `$0.00`
export function formatCost(usd: number): string {
	const abs = Math.abs(usd);
	if (abs > 0 && abs < 0.001) return usd < 0 ? '-<$0.001' : '<$0.001';

	// Decided on the rounded value, so $0.0099 reads `$0.01` rather than `$0.010`
	const subCent = abs > 0 && Math.round(abs * 1000) < 10;
	return currency(usd, subCent ? 3 : 2);
}

// Formats an API cost, which is integer micro-USD (PLAN §11), as e.g. `$0.14`
export function formatMicroCost(micros: number): string {
	return formatCost(micros / 1_000_000);
}

// Formats a chart axis tick without trailing noise, e.g. `$0`, `$0.05`, `$0.10`, `$1` or `$0.005`
// Whole dollars drop their cents, everything else keeps two decimals so neighbouring ticks line up, and sub-cent ticks keep what they need
// Charts that know their tick step can pass it, so an axis of `$0.50` steps reads `$1.00` rather than `$1` next to `$1.50`
export function formatCostTick(usd: number, step?: number): string {
	const abs = Math.abs(usd);
	if (abs === 0) return '$0';
	if (step !== undefined && step > 0) {
		if (Number.isInteger(step)) return currency(usd, 0);
		if (step >= 0.01) return currency(usd, 2);
		return currency(usd, 3, 6);
	}
	if (Number.isInteger(usd)) return currency(usd, 0);
	if (abs >= 0.01) return currency(usd, 2);
	return currency(usd, 3, 6);
}

// Formats an axis tick of micro-USD values, see formatCostTick
export function formatMicroCostTick(micros: number, stepMicros?: number): string {
	return formatCostTick(
		micros / 1_000_000,
		stepMicros === undefined ? undefined : stepMicros / 1_000_000
	);
}

// Formats a token count as e.g. `950`, `12.4k` or `1.2M`
export function formatTokens(count: number): string {
	if (count < 1000) return String(count);
	if (count < 1_000_000) return `${trimZero((count / 1000).toFixed(1))}k`;
	return `${trimZero((count / 1_000_000).toFixed(1))}M`;
}

function trimZero(value: string) {
	return value.endsWith('.0') ? value.slice(0, -2) : value;
}

// Formats a model price, stored as integer micro-USD per 1M tokens, as dollars per 1M tokens, e.g. `$3`, `$0.30`, `$3.75` or `$0.075`
// Whole dollars drop their cents like on price lists, other prices keep cents and any further digits the price has, so it is never rounded
export function formatPricePerMillion(micros: number): string {
	const usd = micros / 1_000_000;
	return Number.isInteger(usd) ? currency(usd, 0) : currency(usd, 2, 6);
}

// Capitalizes the first letter of a backend reason, which starts lowercase because it is also used inside longer messages
export function sentenceCase(text: string): string {
	return text.charAt(0).toUpperCase() + text.slice(1);
}

// Turns an enum value the UI has no label for into sentence case, e.g. `edge_case` into `Edge case`
export function humanize(value: string): string {
	const words = value.replaceAll(/[_-]+/g, ' ').trim();
	return words.charAt(0).toUpperCase() + words.slice(1);
}

// Formats a byte count as e.g. `512 B`, `12.4 MB` or `1.2 GB`
export function formatBytes(bytes: number): string {
	const units = ['B', 'KB', 'MB', 'GB', 'TB'];
	let value = bytes;
	let unit = 0;
	while (value >= 1000 && unit < units.length - 1) {
		value /= 1000;
		unit++;
	}
	return unit === 0 ? `${value} ${units[unit]}` : `${trimZero(value.toFixed(1))} ${units[unit]}`;
}
