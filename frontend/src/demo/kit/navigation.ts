// Stands in for SvelteKit's `$app/navigation` in the demo, handing every navigation to the demo, which decides what it can show
import { onMount } from 'svelte';
import { page } from './state.svelte';

type NavigateHandler = (
	url: URL,
	options: { replaceState?: boolean; noScroll?: boolean }
) => Promise<void>;
type AfterNavigateCallback = () => void;

let handler: NavigateHandler = async () => {};
const afterCallbacks = new Set<AfterNavigateCallback>();

export function setNavigateHandler(next: NavigateHandler) {
	handler = next;
}

// Components that react to navigations, like the sidebar closing on phones, hear about the demo's own ones
export function notifyNavigated() {
	for (const callback of afterCallbacks) callback();
}

export async function goto(
	url: string | URL,
	options: { replaceState?: boolean; noScroll?: boolean; keepFocus?: boolean } = {}
) {
	// Relative links resolve against the page the app believes it is on, not the demo's own address
	await handler(new URL(url, page.url), options);
}

// Loads rerun from the fake server's current state anyway, so there is nothing to invalidate
export async function invalidate() {}
export async function invalidateAll() {}

export function replaceState() {}

// SvelteKit also runs these once the component mounts, which the demo copies
export function afterNavigate(callback: AfterNavigateCallback) {
	onMount(() => {
		afterCallbacks.add(callback);
		callback();
		return () => afterCallbacks.delete(callback);
	});
}
