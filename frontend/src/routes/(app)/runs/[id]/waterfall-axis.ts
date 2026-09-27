import { formatDuration } from '$lib/utils/format-util';

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

// Tick spacings the axis picks from, so ticks land on round durations in every unit
const TICK_STEPS = [
	1,
	2,
	5,
	10,
	20,
	25,
	50,
	100,
	200,
	250,
	500,
	SECOND,
	2 * SECOND,
	5 * SECOND,
	10 * SECOND,
	15 * SECOND,
	20 * SECOND,
	30 * SECOND,
	MINUTE,
	2 * MINUTE,
	5 * MINUTE,
	10 * MINUTE,
	15 * MINUTE,
	20 * MINUTE,
	30 * MINUTE,
	HOUR,
	2 * HOUR,
	3 * HOUR,
	6 * HOUR,
	12 * HOUR,
	DAY
];

export type TimeAxis = {
	// The spacing between two ticks in milliseconds
	step: number;
	// Offsets from the start of the run in milliseconds, starting at 0 and never past the total
	ticks: number[];
	label: (tick: number) => string;
};

// An axis for a run that took `total` milliseconds, with at most `maxIntervals` gaps between its ticks
export function timeAxis(total: number, maxIntervals: number): TimeAxis {
	const step = tickStep(total, Math.max(1, Math.floor(maxIntervals)));

	// Multiplying rather than adding keeps the ticks exact, however many there are
	const ticks: number[] = [];
	for (let i = 0; i * step <= total; i++) ticks.push(i * step);

	return { step, ticks, label: tickFormatter(total, step) };
}

// A duration with the precision a waterfall needs to tell its steps apart, e.g. '731ms', '1.25s', '12.4s' or '1m 20s'
export function formatPreciseDuration(ms: number): string {
	const rounded = Math.round(ms);
	if (rounded < SECOND) return `${rounded}ms`;

	// Short durations keep the decimals that whole seconds would round away, e.g. 1.4s and 1.9s both reading '2s'
	const seconds = ms / SECOND;
	const decimals = seconds < 10 ? 2 : 1;
	const value = Number(seconds.toFixed(decimals));
	if (value < 60) return `${value}s`;
	return formatDuration(ms);
}

// The smallest round spacing that needs no more than `maxIntervals` gaps to cover the run
function tickStep(total: number, maxIntervals: number): number {
	if (!Number.isFinite(total) || total <= 0) return SECOND;
	for (const step of TICK_STEPS) {
		if (total / step <= maxIntervals) return step;
	}

	// Past the list, whole days in a 1, 2, 5 progression
	for (let scale = DAY; ; scale *= 10) {
		for (const factor of [1, 2, 5]) {
			if (total / (scale * factor) <= maxIntervals) return scale * factor;
		}
	}
}

// Labels in one unit per axis, with as many decimals as the spacing needs, so no two ticks read the same
function tickFormatter(total: number, step: number): (tick: number) => string {
	// A run under a second reads best in milliseconds throughout
	if (step < SECOND && total < SECOND) return (tick) => `${tick}ms`;

	// Sub-second spacing past the first second needs decimals, which whole-second rounding would repeat as '1s 1s 2s 2s'
	if (step < SECOND) {
		const decimals = decimalsOf(step / SECOND);
		return (tick) => `${Number((tick / SECOND).toFixed(decimals))}s`;
	}

	// Runs that span days count them, where hours would run into the hundreds
	if (step >= DAY) return (tick) => (tick === 0 ? '0s' : `${tick / DAY}d`);

	// Whole-second spacing is exact in the '1m 30s' style used everywhere else
	return (tick) => (tick === 0 ? '0s' : formatDuration(tick));
}

// The number of decimals a fraction such as 0.25 needs to be written exactly
function decimalsOf(value: number): number {
	const [, fraction = ''] = String(Number(value.toFixed(6))).split('.');
	return fraction.length;
}
