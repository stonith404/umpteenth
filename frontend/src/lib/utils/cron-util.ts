// Cron helpers for schedule inputs and labels: plain-word descriptions, quick picks and validation

const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

// The scheduler's shorthand expressions, in plain words
const DESCRIPTORS: Record<string, string> = {
	'@yearly': 'Yearly on January 1 at 00:00',
	'@annually': 'Yearly on January 1 at 00:00',
	'@monthly': 'Monthly on day 1 at 00:00',
	'@weekly': 'Sundays at 00:00',
	'@daily': 'Daily at 00:00',
	'@midnight': 'Daily at 00:00',
	'@hourly': 'Every hour'
};

// Quick picks for schedule inputs
export const CRON_PRESETS = [
	{ label: 'Every hour', cron: '0 * * * *' },
	{ label: 'Daily at 09:00', cron: '0 9 * * *' },
	{ label: 'Weekdays at 08:00', cron: '0 8 * * 1-5' },
	{ label: 'Mondays at 09:00', cron: '0 9 * * 1' },
	{ label: 'Monthly on day 1 at 09:00', cron: '0 9 1 * *' }
];

// The schedule editor's quick picks, with the sub-hourly intervals monitoring jobs need most in front of the shared presets
export const SCHEDULE_EDITOR_PRESETS = [
	{ label: 'Every 15 minutes', cron: '*/15 * * * *' },
	{ label: 'Every 30 minutes', cron: '*/30 * * * *' },
	...CRON_PRESETS
];

// The bounds and names of the five fields, as the scheduler's parser (robfig/cron) accepts them
const FIELDS = [
	{ min: 0, max: 59 },
	{ min: 0, max: 23 },
	{ min: 1, max: 31 },
	{
		min: 1,
		max: 12,
		names: ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec']
	},
	{ min: 0, max: 6, names: ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'] }
];

// Reports whether the scheduler would accept an expression, so a typo gets flagged before saving
// describeCron only knows common shapes, so this is what tells an unusual but valid expression from a broken one
// The scheduler refuses `@every`, which would allow schedules down to every second, so only the named descriptors pass
export function isValidCron(expression: string): boolean {
	const expr = expression.trim().toLowerCase();
	if (expr.startsWith('@')) return Object.hasOwn(DESCRIPTORS, expr);
	const parts = expr.split(/\s+/);
	return parts.length === FIELDS.length && parts.every((part, i) => isValidField(part, FIELDS[i]));
}

function isValidField(field: string, bounds: (typeof FIELDS)[number]) {
	return field.split(',').every((item) => {
		const [range, step, ...extra] = item.split('/');
		if (extra.length > 0) return false;
		if (step !== undefined && !(/^\d+$/.test(step) && Number(step) > 0)) return false;
		if (range === '*' || range === '?') return true;

		// A single value with a step, e.g. `5/15`, runs from that value to the end of the range
		const [low, high, ...rest] = range.split('-');
		if (rest.length > 0) return false;
		const from = fieldValue(low, bounds);
		const to = high === undefined ? from : fieldValue(high, bounds);
		return from !== null && to !== null && from >= bounds.min && to <= bounds.max && from <= to;
	});
}

function fieldValue(value: string, bounds: (typeof FIELDS)[number]) {
	if (/^\d+$/.test(value)) return Number(value);
	const index = bounds.names?.indexOf(value) ?? -1;
	return index === -1 ? null : bounds.min + index;
}

// Describes common cron expressions in plain words, e.g. `0 8 * * 1-5` as "Weekdays at 08:00"
// Returns null for expressions it doesn't recognize, so callers can fall back to the raw expression
export function describeCron(expression: string): string | null {
	const expr = expression.trim();
	if (!expr) return null;
	if (expr.startsWith('@')) return DESCRIPTORS[expr.toLowerCase()] ?? null;

	const parts = expr.split(/\s+/);
	if (parts.length !== 5) return null;
	const [minute, hour, dom, month, dow] = parts;

	// Sub-hourly and hourly schedules
	// A step only reads as "every N" when it divides the hour or the day, otherwise the gap wraps unevenly at the boundary, e.g. `*/7` fires at :56 and again at :00
	const everyMinutes = /^\*\/(\d+)$/.exec(minute);
	if (everyMinutes && hour === '*' && dom === '*' && month === '*' && dow === '*') {
		const step = Number(everyMinutes[1]);
		if (step === 1) return 'Every minute';
		return isEvenStep(step, 60) ? `Every ${step} minutes` : null;
	}
	if (minute === '*' && hour === '*' && dom === '*' && month === '*' && dow === '*') {
		return 'Every minute';
	}
	if (isInRange(minute, 0, 59) && hour === '*' && dom === '*' && month === '*' && dow === '*') {
		return hourly(minute);
	}
	const everyHours = /^\*\/(\d+)$/.exec(hour);
	if (isInRange(minute, 0, 59) && everyHours && dom === '*' && month === '*' && dow === '*') {
		const step = Number(everyHours[1]);
		if (step === 1) return hourly(minute);
		if (!isEvenStep(step, 24)) return null;
		return Number(minute) === 0 ? `Every ${step} hours` : `Every ${step} hours at :${pad(minute)}`;
	}

	// Everything below fires at one fixed time of day
	if (!isInRange(minute, 0, 59) || !isInRange(hour, 0, 23) || month !== '*') return null;
	const time = `${pad(hour)}:${pad(minute)}`;

	if (dom === '*' && dow === '*') return `Daily at ${time}`;
	if (dom === '*') {
		const days = describeDays(dow);
		return days ? `${days} at ${time}` : null;
	}
	if (isInRange(dom, 1, 31) && dow === '*') return `Monthly on day ${Number(dom)} at ${time}`;
	return null;
}

// On the hour reads as "Every hour", like the preset, and any other minute names it
function hourly(minute: string) {
	return Number(minute) === 0 ? 'Every hour' : `Hourly at :${pad(minute)}`;
}

function describeDays(dow: string): string | null {
	const upper = dow.toUpperCase();
	if (upper === 'MON-FRI') return 'Weekdays';
	if (upper === 'SAT,SUN' || upper === 'SUN,SAT') return 'Weekends';
	if (dow === '1-5') return 'Weekdays';

	// A list of single days, e.g. `1,3,5`, where the scheduler only accepts 0 to 6 with 0 as Sunday
	const parts = dow.split(',');
	if (!parts.every((d) => isInRange(d, 0, 6))) return null;
	const days = [...new Set(parts.map(Number))].sort((a, b) => a - b);

	// Some lists are common enough to have a name of their own
	const key = days.join(',');
	if (key === '1,2,3,4,5') return 'Weekdays';
	if (key === '0,6') return 'Weekends';
	if (key === '0,1,2,3,4,5,6') return 'Daily';

	const names = days.map((d) => `${DAY_NAMES[d]}s`);
	if (names.length === 1) return names[0];
	return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}

function isNumber(value: string) {
	return /^\d+$/.test(value);
}

function isInRange(value: string, min: number, max: number) {
	return isNumber(value) && Number(value) >= min && Number(value) <= max;
}

// A step repeats at an even interval only when it is smaller than the period and divides it
function isEvenStep(step: number, period: number) {
	return step > 0 && step < period && period % step === 0;
}

function pad(value: string) {
	return String(Number(value)).padStart(2, '0');
}
