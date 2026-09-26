import { createSubscriber } from 'svelte/reactivity';

// A reactive "now" that only ticks while something reads it, so relative times and timers stay current without idle intervals
export class Clock {
	#subscribe: () => void;

	constructor(intervalMs: number) {
		this.#subscribe = createSubscriber((update) => {
			const interval = setInterval(update, intervalMs);
			return () => clearInterval(interval);
		});
	}

	get now() {
		this.#subscribe();
		return Date.now();
	}
}

// Relative times like "3 minutes ago" only need to change a few times a minute
export const relativeTimeClock = new Clock(15_000);
