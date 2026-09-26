// Delays calls to `func` until `delay` ms passed without another call
// The returned function also exposes `cancel` and `pending`, so callers can drop or detect a scheduled call
export function debounced<T extends (...args: any[]) => any>(
	func: T,
	delay: number,
	onLoadingChange?: (loading: boolean) => void
) {
	let timeout: ReturnType<typeof setTimeout> | undefined;

	const run = (...args: Parameters<T>) => {
		if (timeout !== undefined) {
			clearTimeout(timeout);
		}

		onLoadingChange?.(true);

		timeout = setTimeout(async () => {
			timeout = undefined;
			try {
				await func(...args);
			} finally {
				onLoadingChange?.(false);
			}
		}, delay);
	};

	return Object.assign(run, {
		cancel() {
			if (timeout === undefined) return;
			clearTimeout(timeout);
			timeout = undefined;
			onLoadingChange?.(false);
		},
		get pending() {
			return timeout !== undefined;
		}
	});
}
