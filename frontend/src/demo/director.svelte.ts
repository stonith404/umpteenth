// Decides which run the demo shows: it tells the story of the Hacker News digest on its own, and hands control to the reader once they act
import type { RunDetail } from '$lib/api/types';
import { clock } from './clock';
import { server, scenarios, type ScenarioKey } from './fake-server';
import { user } from './fixtures';
import { notifyNavigated, setNavigateHandler } from './kit/navigation';
import { DEMO_ORIGIN, page } from './kit/state.svelte';

// Pauses of the story on the demo's clock, which stands still while the demo is off screen
const SETTLE_MS = 1200;
const HOLD_MS: Record<ScenarioKey, number> = { explore: 5000, scripted: 6500 };

// Messages to and from the docs page that embeds the demo, which shows a switch between the two runs
const MESSAGE_SOURCE = 'umpteenth-demo';
type HostMessage = { source: typeof MESSAGE_SOURCE; type: 'select'; key: ScenarioKey };

const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;

class Director {
	// The run the page shows, and a counter that remounts the page for a fresh replay of the same run
	run = $state.raw<RunDetail | null>(null);
	mount = $state(0);

	#auto = !reducedMotion;
	// A replay of the same run keeps its ID, so the playback itself tells an old replay from the one on screen
	#shown: object | null = null;

	start() {
		setNavigateHandler((url) => this.#navigate(url));
		this.#listenToHost();
		this.#stopOnInteraction();

		// Without motion the demo shows the first run finished and waits for the reader
		if (reducedMotion) void this.show('explore', false);
		else void this.#story();
	}

	// Plays a run from its start, or shows it finished, and resolves once it ended
	async show(key: ScenarioKey, live: boolean) {
		const playback = server.play(key, live);
		await this.#open(playback.id);
		await playback.done;
	}

	// The exploring run, then the scripted one, over and over until the reader takes over
	async #story() {
		const keys: ScenarioKey[] = ['explore', 'scripted'];
		for (let i = 0; this.#auto; i = (i + 1) % keys.length) {
			const key = keys[i];
			await this.show(key, true);
			if (!this.#auto) return;

			// The finished header says what the run cost and how long it took, so the page scrolls back up to it
			await this.#hold(SETTLE_MS);
			if (!this.#auto) return;
			document.scrollingElement?.scrollTo({ top: 0, behavior: 'smooth' });
			await this.#hold(HOLD_MS[key]);
		}
	}

	#hold(ms: number) {
		return server.waitUntil(clock.now() + ms);
	}

	// Shows a run the way the app's router would: new URL, new page data, the page mounted afresh
	async #open(id: string) {
		const playback = server.get(id);
		if (!playback) return;
		const run = playback.detail();

		// A live run replays at its recording's speed, and the pauses after it at the normal one
		this.#shown = playback;
		clock.setRate(playback.ended ? 1 : scenarios[playback.key].rate);
		void playback.done.then(() => {
			if (this.#shown === playback) clock.setRate(1);
		});

		page.url = new URL(`/runs/${id}`, DEMO_ORIGIN);
		page.params = { id };
		page.route = { id: '/(app)/runs/[id]' };
		page.data = { user, run, breadcrumbLabels: { [id]: `${run.jobName} #${run.number}` } };
		this.run = run;
		this.mount++;
		document.scrollingElement?.scrollTo({ top: 0 });
		notifyNavigated();
		this.#tellHost(id);
	}

	// Tabs change the query of the current page and a retry opens the new run, every other navigation stays put
	async #navigate(url: URL) {
		if (url.pathname === page.url.pathname) {
			page.url = url;
			return;
		}
		const id = url.pathname.match(/^\/runs\/([^/]+)$/)?.[1];
		if (id && server.get(id)) {
			this.#auto = false;
			await this.#open(id);
		}
	}

	// The first click or key press in the demo is the reader exploring, so the story stops switching runs under them
	#stopOnInteraction() {
		const stop = () => (this.#auto = false);
		addEventListener('pointerdown', stop, { capture: true, once: true });
		addEventListener('keydown', stop, { capture: true, once: true });
	}

	#listenToHost() {
		addEventListener('message', (event: MessageEvent<HostMessage>) => {
			if (event.origin !== location.origin || event.data?.source !== MESSAGE_SOURCE) return;
			if (event.data.type !== 'select' || !(event.data.key in scenarios)) return;
			this.#auto = false;
			void this.show(event.data.key, !reducedMotion);
		});
	}

	// The switch under the demo marks the run on screen, and neither run after a retry
	#tellHost(id: string) {
		if (window.parent === window) return;
		const key =
			(Object.keys(scenarios) as ScenarioKey[]).find((k) => scenarios[k].recording.run.id === id) ??
			null;
		window.parent.postMessage({ source: MESSAGE_SOURCE, type: 'shown', key }, location.origin);
	}
}

export const director = new Director();
