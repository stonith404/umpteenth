// Delays calls to `func` until `delay` ms passed without another call
// The returned function also exposes `cancel` and `pending`, so callers can drop or detect a scheduled call
export function debounced<Args extends unknown[]>(func: (...args: Args) => unknown, delay: number) {
	let timeout: ReturnType<typeof setTimeout> | undefined;

	const run = (...args: Args) => {
		clearTimeout(timeout);
		timeout = setTimeout(() => {
			timeout = undefined;
			void func(...args);
		}, delay);
	};

	return Object.assign(run, {
		cancel() {
			clearTimeout(timeout);
			timeout = undefined;
		},
		get pending() {
			return timeout !== undefined;
		}
	});
}
