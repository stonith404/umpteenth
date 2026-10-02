// The backend of the demo: it answers the app's requests and streams from recorded runs, so the real pages render without a server
// A live replay sends the recorded events at their recorded moments on the demo's clock, with the streaming deltas a real run sends in between
import type { Run, RunDelta, RunDetail, RunEvent, WorkspaceEvent } from '#lib/api/types.js';
import { clock } from './clock';
import { exploreRun, scriptedRun, type RecordedRun } from './fixtures';

export type ScenarioKey = 'explore' | 'scripted';

// Playback speed of each recording: the exploring run is sped up so it fits a hero, the scripted one slowed down so it can be followed at all
export const scenarios: Record<ScenarioKey, { recording: RecordedRun; rate: number }> = {
	explore: { recording: exploreRun, rate: 3 },
	scripted: { recording: scriptedRun, rate: 0.5 }
};

// Reflection runs after a run ends, and this is how long it takes on the demo's clock
const REFLECTION_MS = 3500;
// A finished run that isn't replayed ended this long before the page opened
const FINISHED_AGO_MS = 4 * 60_000;
// Requests take a moment like over a network, so loading states show the way they do in the app
const RESPONSE_DELAY_MS = 40;
const TICK_MS = 40;

type StreamMessage = { name: 'event' | 'delta' | 'status' | 'end'; data?: unknown };
type Item = { at: number; fire: () => void };

// One run as the demo shows it, replayed live or already finished
class Playback {
	readonly id: string;
	readonly number: number;
	readonly key: ScenarioKey;
	readonly recording: RecordedRun;
	readonly emitted: RunEvent[] = [];
	status = 'queued';
	ended = false;
	reflection: RunDetail['reflection'] = 'skipped';
	learnedAgain = false;
	readonly done: Promise<void>;

	#offset: number;
	#items: Item[] = [];
	#finishedAt: number | null = null;
	#resolveDone!: () => void;
	#streams = new Set<FakeEventSource>();

	constructor(key: ScenarioKey, id: string, number: number, live: boolean) {
		this.key = key;
		this.id = id;
		this.number = number;
		this.recording = scenarios[key].recording;
		this.done = new Promise((resolve) => (this.#resolveDone = resolve));

		// Every recorded timestamp moves by one offset, so the run starts now or finished a few minutes ago
		const run = this.recording.run;
		this.#offset = live
			? clock.now() - run.queuedAt
			: clock.now() - FINISHED_AGO_MS - (run.finishedAt ?? run.queuedAt);
		const events = this.recording.events.map((event) => ({
			...event,
			ts: event.ts + this.#offset
		}));

		if (!live) {
			this.emitted.push(...events);
			this.status = run.status;
			this.reflection = run.reflection;
			this.#finish(events.at(-1)?.ts ?? clock.now());
			return;
		}
		this.#schedule(events);
	}

	// The recorded events, the deltas a live run streams between them and the reflection after the end
	#schedule(events: RunEvent[]) {
		const items: Item[] = [];
		for (const event of events) {
			items.push({ at: event.ts, fire: () => this.#emit(event) });
			items.push(...this.#deltasBefore(event, events));
		}
		const end = (events.at(-1)?.ts ?? clock.now()) + 1;
		items.push({ at: end, fire: () => this.#end(end) });
		if (this.recording.run.reflection === 'done') {
			items.push({ at: end + REFLECTION_MS, fire: () => this.#reflect('done') });
		}
		this.#items = items.sort((a, b) => a.at - b.at);
	}

	// A model turn streams its reasoning and text while it runs, and a command its output, both replaced by the persisted event when it lands
	#deltasBefore(event: RunEvent, events: RunEvent[]): Item[] {
		const payload = event.payload as Record<string, unknown>;
		if (event.type === 'llm.call') {
			const start = event.ts - (event.ms ?? 0);
			const reasoning = String(payload.reasoning ?? '');
			const text = String(payload.text ?? '');
			const split = reasoning ? start + (event.ts - start) * 0.4 : start;
			const calls = (payload.toolCalls as { name: string }[] | null) ?? [];
			return [
				...this.#chunks(reasoning, start + 150, split, (chunk) => ({
					type: 'reasoning',
					text: chunk
				})),
				...this.#chunks(text, split + 100, event.ts - 250, (chunk) => ({
					type: 'text',
					text: chunk
				})),
				...calls.map((call, i) => ({
					at: event.ts - 200 + i * 20,
					fire: () => this.#send({ name: 'delta', data: { type: 'tool_call', text: call.name } })
				}))
			];
		}
		if (
			event.type === 'tool.result' &&
			typeof payload.content === 'string' &&
			payload.content.startsWith('exit code')
		) {
			const call = events.find(
				(e) =>
					e.type === 'tool.call' && (e.payload as { callId?: string }).callId === payload.callId
			);
			if (!call) return [];
			const output = payload.content.slice(payload.content.indexOf('\n') + 1);
			const callId = String(payload.callId);
			return this.#chunks(
				output,
				call.ts + 60,
				event.ts - 30,
				(chunk) => ({ type: 'tool_output', callId, text: chunk }),
				true
			);
		}
		return [];
	}

	// Spreads a text over a window of time, three words or one line at a time
	#chunks(
		text: string,
		from: number,
		to: number,
		delta: (chunk: string) => RunDelta,
		byLine = false
	): Item[] {
		if (!text) return [];
		const parts = text.split(byLine ? /(?<=\n)/ : /(?<=\s)/);
		const size = byLine ? 1 : 3;
		const chunks: string[] = [];
		for (let i = 0; i < parts.length; i += size) chunks.push(parts.slice(i, i + size).join(''));
		const step = Math.max(0, to - from) / chunks.length;
		return chunks.map((chunk, i) => ({
			at: from + i * step,
			fire: () => this.#send({ name: 'delta', data: delta(chunk) })
		}));
	}

	#emit(event: RunEvent) {
		this.emitted.push(event);
		this.#send({ name: 'event', data: event });
		if (event.type !== 'run.status') return;
		this.status = String((event.payload as { status: string }).status);
		this.#send({ name: 'status', data: { status: this.status } });
		server.broadcast({
			kind: 'run',
			runId: this.id,
			jobId: this.recording.run.jobId,
			status: this.status
		});
	}

	#end(at: number) {
		this.#finish(at);
		this.#send({ name: 'end' });
		for (const stream of this.#streams) stream.close();
		if (this.recording.run.reflection === 'done' && this.status === 'succeeded')
			this.#reflect('pending');
	}

	#finish(at: number) {
		this.ended = true;
		this.#finishedAt = at;
		this.#resolveDone();
	}

	#reflect(status: 'pending' | 'done') {
		this.reflection = status;
		server.broadcast({
			kind: 'reflection',
			runId: this.id,
			jobId: this.recording.run.jobId,
			status
		});
	}

	// Plays whatever is due on the demo's clock
	tick(now: number) {
		while (this.#items.length > 0 && this.#items[0].at <= now) this.#items.shift()!.fire();
	}

	// Replaced by another replay of the same run, which must not keep sending into the page
	discard() {
		this.#items = [];
		for (const stream of this.#streams) stream.close();
		this.#finish(clock.now());
	}

	// Stopping ends the run the way the runner does, with the sandbox destroyed and the status cancelled
	cancel() {
		if (this.ended) return;
		const now = clock.now();
		this.#items = [];
		const seq = (this.emitted.at(-1)?.seq ?? 0) + 1;
		const destroy = this.recording.events.find((e) => e.type === 'sandbox.destroy');
		if (destroy) this.#emit({ ...destroy, seq, ts: now });
		this.#emit({
			seq: seq + 1,
			ts: now,
			type: 'run.status',
			spanId: null,
			ms: null,
			payload: { status: 'cancelled' }
		});
		this.#end(now);
	}

	learn() {
		this.learnedAgain = true;
		this.#reflect('pending');
		const at = clock.now() + REFLECTION_MS;
		this.#items.push({ at, fire: () => this.#reflect('done') });
	}

	subscribe(stream: FakeEventSource, after: number) {
		for (const event of this.emitted) if (event.seq > after) stream.send('event', event);
		if (this.ended) {
			stream.send('end');
			return;
		}
		this.#streams.add(stream);
	}

	unsubscribe(stream: FakeEventSource) {
		this.#streams.delete(stream);
	}

	#send(message: StreamMessage) {
		for (const stream of this.#streams) stream.send(message.name, message.data);
	}

	// The run as `GET /api/runs/{id}` returns it at this moment
	detail(): RunDetail {
		const run = this.recording.run;
		const shift = (ts: number | null) => (ts === null ? null : ts + this.#offset);
		const base: RunDetail = {
			...run,
			id: this.id,
			number: this.number,
			queuedAt: run.queuedAt + this.#offset,
			startedAt: this.status === 'queued' ? null : shift(run.startedAt),
			status: this.status
		};
		if (!this.ended) {
			// The run row only gets its totals and results when the run ends
			return {
				...base,
				finishedAt: null,
				msTotal: null,
				msLlm: 0,
				msTools: 0,
				turns: 0,
				tokIn: 0,
				tokOut: 0,
				tokCacheRead: 0,
				tokCacheWrite: 0,
				cost: 0,
				summary: null,
				outputs: null,
				reflection: 'skipped',
				reflectionSummary: null,
				reflectionOps: [],
				reflectionVersion: null,
				reflectionCost: 0,
				reflectionTokens: 0
			};
		}
		if (this.status === 'cancelled') {
			// A stopped run only counts the work it did before the stop, and a run stopped in the queue never started
			const finishedAt = this.#finishedAt ?? clock.now();
			const started = this.emitted.some(
				(e) => e.type === 'run.status' && (e.payload as { status: string }).status !== 'cancelled'
			);
			return {
				...base,
				startedAt: started ? base.startedAt : null,
				msQueue: started ? base.msQueue : null,
				finishedAt,
				msTotal: finishedAt - base.queuedAt,
				summary: null,
				outputs: null,
				error: 'Cancelled by Ada Park',
				reflection: 'skipped',
				reflectionOps: [],
				reflectionSummary: null,
				reflectionVersion: null,
				reflectionCost: 0,
				reflectionTokens: 0,
				...this.#totalsSoFar()
			};
		}
		const reflected = this.reflection === 'done';
		return {
			...base,
			finishedAt: shift(run.finishedAt),
			reflection: this.reflection,
			// A run learned from again finds nothing the playbook doesn't already say
			reflectionSummary: reflected
				? this.learnedAgain
					? 'Nothing new to learn: the run went the way the playbook describes.'
					: run.reflectionSummary
				: null,
			reflectionOps: reflected && !this.learnedAgain ? run.reflectionOps : [],
			reflectionVersion: reflected && !this.learnedAgain ? run.reflectionVersion : null,
			reflectionCost: reflected ? run.reflectionCost : 0,
			reflectionTokens: reflected ? run.reflectionTokens : 0
		};
	}

	// The run's totals from the events it emitted so far, like the runner adds them up when a run ends early
	#totalsSoFar() {
		const totals = {
			cost: 0,
			turns: 0,
			tokIn: 0,
			tokOut: 0,
			tokCacheRead: 0,
			tokCacheWrite: 0,
			msLlm: 0,
			msTools: 0,
			msProvision: null as number | null
		};
		for (const event of this.emitted) {
			if (event.type === 'sandbox.create') totals.msProvision = event.ms;
			if (event.type === 'tool.result') totals.msTools += event.ms ?? 0;
			if (event.type !== 'llm.call') continue;
			const payload = event.payload as {
				cost?: number;
				latencyMs?: number;
				usage?: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number };
			};
			totals.cost += payload.cost ?? 0;
			totals.tokIn += payload.usage?.input ?? 0;
			totals.tokOut += payload.usage?.output ?? 0;
			totals.tokCacheRead += payload.usage?.cacheRead ?? 0;
			totals.tokCacheWrite += payload.usage?.cacheWrite ?? 0;
			totals.msLlm += payload.latencyMs ?? 0;
			totals.turns++;
		}
		return totals;
	}

	summary(): Run {
		const d = this.detail();
		return {
			id: d.id,
			jobId: d.jobId,
			jobName: d.jobName,
			number: d.number,
			status: d.status,
			mode: d.mode,
			trigger: d.trigger,
			queuedAt: d.queuedAt,
			startedAt: d.startedAt,
			finishedAt: d.finishedAt,
			msQueue: d.msQueue,
			msProvision: d.msProvision,
			msLlm: d.msLlm,
			msTools: d.msTools,
			msTotal: d.msTotal,
			turns: d.turns,
			tokIn: d.tokIn,
			tokOut: d.tokOut,
			tokCacheRead: d.tokCacheRead,
			tokCacheWrite: d.tokCacheWrite,
			cost: d.cost,
			modelName: d.modelName,
			modelLabel: d.modelLabel,
			summary: d.summary,
			error: d.error,
			sandboxIsolation: d.sandboxIsolation
		};
	}
}

// The requests the app makes on the run page, from its sidebar and from the command palette
class FakeServer {
	#runs = new Map<string, Playback>();
	#workspaceStreams = new Set<FakeEventSource>();
	#waiters: { at: number; resolve: () => void }[] = [];

	constructor() {
		setInterval(() => this.#tick(), TICK_MS);
	}

	// Starts a run of a recording, replacing an earlier replay of the same run
	play(key: ScenarioKey, live: boolean): Playback {
		const run = scenarios[key].recording.run;
		this.#runs.get(run.id)?.discard();
		const playback = new Playback(key, run.id, run.number, live);
		this.#runs.set(run.id, playback);
		return playback;
	}

	// A retry is a new run of the job, numbered after the latest one
	#retry(): Playback {
		const number = Math.max(...[...this.#runs.values()].map((p) => p.number)) + 1;
		const playback = new Playback('scripted', crypto.randomUUID(), number, true);
		this.#runs.set(playback.id, playback);
		return playback;
	}

	get(id: string) {
		return this.#runs.get(id);
	}

	// Resolves once the demo's clock reaches a moment, so pauses in the story stop with the clock
	waitUntil(at: number): Promise<void> {
		return new Promise((resolve) => this.#waiters.push({ at, resolve }));
	}

	broadcast(event: WorkspaceEvent) {
		for (const stream of this.#workspaceStreams) stream.send('run', event);
	}

	#tick() {
		const now = clock.now();
		for (const playback of this.#runs.values()) playback.tick(now);
		const due = this.#waiters.filter((w) => w.at <= now);
		this.#waiters = this.#waiters.filter((w) => w.at > now);
		for (const waiter of due) waiter.resolve();
	}

	connect(stream: FakeEventSource) {
		const url = new URL(stream.url, location.href);
		const runId = url.pathname.match(/^\/api\/runs\/([^/]+)\/stream$/)?.[1];
		// Opening is asynchronous in a browser too, so listeners attached after the constructor still hear it
		setTimeout(() => {
			if (stream.readyState === FakeEventSource.CLOSED) return;
			stream.open();
			if (url.pathname === '/api/events') this.#workspaceStreams.add(stream);
			else if (runId)
				this.#runs.get(runId)?.subscribe(stream, Number(url.searchParams.get('after') ?? 0));
		});
	}

	disconnect(stream: FakeEventSource) {
		this.#workspaceStreams.delete(stream);
		for (const playback of this.#runs.values()) playback.unsubscribe(stream);
	}

	async handle(request: Request): Promise<Response> {
		await new Promise((resolve) => setTimeout(resolve, RESPONSE_DELAY_MS));
		const url = new URL(request.url);
		const path = url.pathname;
		const query = url.searchParams;
		const method = request.method;

		// The run page
		const runRoute = path.match(/^\/api\/runs\/([^/]+)(\/[a-z]+)?$/);
		if (runRoute) {
			const playback = this.#runs.get(runRoute[1]);
			if (!playback) return notFound('Run');
			const action = runRoute[2] ?? '';
			if (method === 'GET' && action === '') return json(playback.detail());
			if (method === 'GET' && action === '/events') {
				const after = Number(query.get('after') ?? 0);
				const limit = Number(query.get('limit') ?? 2000);
				return json(playback.emitted.filter((e) => e.seq > after).slice(0, limit));
			}
			if (method === 'GET' && action === '/artifacts')
				return json(
					playback.ended && playback.status === 'succeeded' ? playback.recording.artifacts : []
				);
			if (method === 'POST' && action === '/cancel') {
				playback.cancel();
				return empty();
			}
			if (method === 'POST' && action === '/learn') {
				playback.learn();
				return empty();
			}
			if (method === 'POST' && action === '/retry') {
				// A retry today runs the graduated job, so it replays the scripted run
				const retry = this.#retry();
				return json({ runId: retry.id, status: 'queued' });
			}
		}

		// Runs in progress, for the sidebar's running count
		if (method === 'GET' && path === '/api/runs') {
			const statuses = query.get('status')?.split(',');
			const items = [...this.#runs.values()]
				.map((p) => p.summary())
				.filter((r) => !statuses || statuses.includes(r.status));
			return json({ items, page: 1, pageSize: items.length, total: items.length });
		}

		// The playbook version run #1 created, for the diff on its Learned tab
		const version = exploreRun.version;
		const versionPath =
			version && `/api/jobs/${exploreRun.run.jobId}/playbook/versions/${version.version}`;
		if (method === 'GET' && path === versionPath) return json(version);

		return notFound('Page');
	}
}

export const server = new FakeServer();

// A server-sent event stream fed by the fake server instead of the network
export class FakeEventSource extends EventTarget {
	static readonly CONNECTING = 0;
	static readonly OPEN = 1;
	static readonly CLOSED = 2;
	readonly url: string;
	readyState: number = FakeEventSource.CONNECTING;

	constructor(url: string | URL) {
		super();
		this.url = String(url);
		server.connect(this);
	}

	open() {
		this.readyState = FakeEventSource.OPEN;
		this.dispatchEvent(new Event('open'));
	}

	send(name: string, data?: unknown) {
		if (this.readyState !== FakeEventSource.OPEN) return;
		this.dispatchEvent(
			new MessageEvent(name, { data: data === undefined ? '' : JSON.stringify(data) })
		);
	}

	close() {
		this.readyState = FakeEventSource.CLOSED;
		server.disconnect(this);
	}
}

// Requests to the API go to the fake server and its streams, everything else such as fonts to the network
export function installFakeNetwork() {
	const realFetch = window.fetch.bind(window);
	window.fetch = (input, init) => {
		const request = new Request(input, init);
		if (!new URL(request.url).pathname.startsWith('/api/')) return realFetch(input, init);
		return server.handle(request);
	};
	window.EventSource = FakeEventSource as unknown as typeof EventSource;
}

function json(body: unknown, status = 200) {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json' }
	});
}

function empty() {
	return new Response(null, { status: 204 });
}

// Everything the demo leaves out answers like a missing resource, which the app already handles
function notFound(resource: string) {
	return json({ code: 'not_found', message: `${resource} not found`, details: { resource } }, 404);
}
