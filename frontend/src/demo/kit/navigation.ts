// Stands in for SvelteKit's `$app/navigation` in the demo, handing every navigation to the demo, which decides what it can show
import { onMount } from 'svelte';
import { page } from './state.svelte';

type GotoOptions = { replace?: boolean; reset?: boolean; refreshAll?: boolean; shallow?: boolean };
type NavigateHandler = (url: URL, options: GotoOptions) => Promise<void>;
type AfterNavigateCallback = (navigation: { shallow: boolean }) => void;

let handler: NavigateHandler = async () => {};
const afterCallbacks = new Set<AfterNavigateCallback>();

export function setNavigateHandler(next: NavigateHandler) {
	handler = next;
}

// Components that react to navigations, like the sidebar closing on phones, hear about the demo's own ones
export function notifyNavigated() {
	for (const callback of afterCallbacks) callback({ shallow: false });
}

export async function goto(url: string | URL, options: GotoOptions = {}) {
	// Shallow navigations only change the URL of the current page, which the demo's address never shows
	if (options.shallow) return;
	// Relative links resolve against the page the app believes it is on, not the demo's own address
	await handler(new URL(url, page.url), options);
}

// Loads rerun from the fake server's current state anyway, so there is nothing to invalidate
export async function invalidate() {}
export async function refreshAll() {}

// SvelteKit also runs these once the component mounts, which the demo copies
export function afterNavigate(callback: AfterNavigateCallback) {
	onMount(() => {
		afterCallbacks.add(callback);
		callback({ shallow: false });
		return () => afterCallbacks.delete(callback);
	});
}
