// Runs before any of the app's modules, which main.ts imports after this one
// Time and network are in place before the app reads the clock or makes a request, and index.html has already set the color mode
import { installClock } from './clock';
import { installFakeNetwork } from './fake-server';

installClock();
installFakeNetwork();

// The docs page around the demo, which is on the same origin when embedded and missing when the demo is opened on its own
export function hostRoot(): HTMLElement | null {
	try {
		return window.parent !== window ? window.parent.document.documentElement : null;
	} catch {
		return null;
	}
}

// The docs' theme, or the one in the demo's URL when it runs on its own
export function hostTheme(): 'light' | 'dark' | null {
	const theme = hostRoot()?.dataset.theme ?? new URLSearchParams(location.search).get('theme');
	return theme === 'light' || theme === 'dark' ? theme : null;
}
