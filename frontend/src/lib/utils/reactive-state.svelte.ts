// Wraps a value in deeply reactive state, so reads inside `$derived` and `$effect` are tracked even when it is mutated in place later
// It lives in its own module because runes are only available in `.svelte.ts` files
export function reactiveState<T>(value: T): T {
	const state = $state(value);
	return state;
}
