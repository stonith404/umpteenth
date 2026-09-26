// Formatting helpers for the visual language in PLAN §13
// Timestamps from the API are unix milliseconds

const LOCALE = 'en-US';

const dateTimeFormat = new Intl.DateTimeFormat(LOCALE, { dateStyle: 'medium', timeStyle: 'short' });
const dateFormat = new Intl.DateTimeFormat(LOCALE, { dateStyle: 'medium' });
const relativeFormat = new Intl.RelativeTimeFormat(LOCALE, { numeric: 'auto' });

const RELATIVE_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
	['year', 365 * 24 * 60 * 60 * 1000],
	['month', 30 * 24 * 60 * 60 * 1000],
	['week', 7 * 24 * 60 * 60 * 1000],
	['day', 24 * 60 * 60 * 1000],
	['hour', 60 * 60 * 1000],
	['minute', 60 * 1000],
	['second', 1000]
];

// Formats a timestamp as e.g. `Sep 25, 2026, 9:06 PM`
export function formatDateTime(ms: number): string {
	return dateTimeFormat.format(ms);
}

// Formats a timestamp as e.g. `Sep 25, 2026`
export function formatDate(ms: number): string {
	return dateFormat.format(ms);
}

// Formats a timestamp relative to `now`, e.g. `3 minutes ago` or `in 2 days`
export function formatRelative(ms: number, now = Date.now()): string {
	const diff = ms - now;
	if (Math.abs(diff) < 10_000) return 'just now';

	for (const [unit, unitMs] of RELATIVE_UNITS) {
		if (Math.abs(diff) >= unitMs || unit === 'second') {
			return relativeFormat.format(Math.round(diff / unitMs), unit);
		}
	}
	return formatDateTime(ms);
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

// Formats a USD amount as e.g. `$0.014`, keeping sub-cent precision for LLM costs
export function formatCost(usd: number): string {
	const digits = usd !== 0 && Math.abs(usd) < 1 ? 3 : 2;
	return `$${usd.toFixed(digits)}`;
}

// Formats an API cost, which is integer micro-USD (PLAN §11), as e.g. `$0.014`
export function formatMicroCost(micros: number): string {
	return formatCost(micros / 1_000_000);
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

// Formats a model price, stored as integer micro-USD per 1M tokens, as dollars per 1M tokens, e.g. `$3` or `$0.25`
export function formatPricePerMillion(micros: number): string {
	const usd = micros / 1_000_000;
	return `$${Number(usd.toFixed(usd < 1 ? 3 : 2))}`;
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
