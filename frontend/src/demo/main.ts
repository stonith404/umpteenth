// The landing page's live demo: the app's real run page on a fake server that replays recorded runs
// Built on its own with `pnpm build:demo`, and embedded by the docs in an iframe
import { hostRoot, hostTheme } from './setup';
import { mount } from 'svelte';
import { setMode } from 'mode-watcher';
import { clock } from './clock';
import Demo from './demo.svelte';
import { director } from './director.svelte';

// The demo follows the docs' theme when the reader switches it there
const host = hostRoot();
if (host) {
	new MutationObserver(() => {
		const theme = hostTheme();
		if (theme) setMode(theme);
	}).observe(host, { attributes: true, attributeFilter: ['data-theme'] });
}

// The app's links point at pages the demo doesn't have, so following one would load a missing page into the frame
document.addEventListener('click', (event) => {
	const href = (event.target as Element | null)?.closest('a[href]')?.getAttribute('href');
	if (href && !href.startsWith('#')) event.preventDefault();
});

// The story only moves while the reader can see it, so it never plays to an empty room
// A fixed element the size of the frame is watched rather than the page, whose visible share shrinks as the timeline grows
const viewport = Object.assign(document.createElement('div'), { ariaHidden: 'true' });
viewport.style.cssText = 'position: fixed; inset: 0; pointer-events: none; visibility: hidden';
document.documentElement.append(viewport);
let onScreen = false;
const updatePause = () => clock.setPaused(!onScreen || document.hidden);
new IntersectionObserver(
	([entry]) => {
		onScreen = entry.isIntersecting && entry.intersectionRatio >= 0.2;
		updatePause();
	},
	{ threshold: [0, 0.2] }
).observe(viewport);
document.addEventListener('visibilitychange', updatePause);
updatePause();

director.start();
mount(Demo, { target: document.body });
