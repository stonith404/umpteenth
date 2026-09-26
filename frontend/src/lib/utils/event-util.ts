// Wraps an event handler so the browser's default action (e.g. a form submit reload) never runs
export function preventDefault(fn: (event: Event) => void): (event: Event) => void {
	return function (this: unknown, event) {
		event.preventDefault();
		fn.call(this, event);
	};
}
