// Describes common cron expressions in plain words, e.g. `0 8 * * 1-5` as "Weekdays at 08:00"
// Returns null for expressions it doesn't recognize, so callers can fall back to the raw expression

const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

const DESCRIPTORS: Record<string, string> = {
	'@yearly': 'Yearly on January 1 at 00:00',
	'@annually': 'Yearly on January 1 at 00:00',
	'@monthly': 'Monthly on day 1 at 00:00',
	'@weekly': 'Sundays at 00:00',
	'@daily': 'Daily at 00:00',
	'@midnight': 'Daily at 00:00',
	'@hourly': 'Hourly at :00'
};

// Quick picks for schedule inputs
export const CRON_PRESETS = [
	{ label: 'Every hour', cron: '0 * * * *' },
	{ label: 'Daily at 09:00', cron: '0 9 * * *' },
	{ label: 'Weekdays at 08:00', cron: '0 8 * * 1-5' },
	{ label: 'Mondays at 09:00', cron: '0 9 * * 1' },
	{ label: 'Monthly on day 1 at 09:00', cron: '0 9 1 * *' }
];

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
		return `Hourly at :${pad(minute)}`;
	}
	const everyHours = /^\*\/(\d+)$/.exec(hour);
	if (isInRange(minute, 0, 59) && everyHours && dom === '*' && month === '*' && dow === '*') {
		const step = Number(everyHours[1]);
		if (step === 1) return `Hourly at :${pad(minute)}`;
		return isEvenStep(step, 24) ? `Every ${step} hours at :${pad(minute)}` : null;
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
