import type { RunDelta, RunEvent } from '$lib/api/types';
import RunService from '$lib/services/run-service';
import { payloadOf, type ToolCallPayload } from './timeline-model';

// Persisted events are fetched in pages of this size before the live stream takes over
const EVENTS_PAGE_SIZE = 2000;
// A stream the server refused is retried with a doubling delay, so an outage or an expired session isn't hammered every few seconds
const RECONNECT_BASE_MS = 3000;
const RECONNECT_MAX_MS = 60_000;
// Live output is a preview until the tool result arrives, so only its tail is kept in memory
const MAX_LIVE_OUTPUT_CHARS = 200_000;

type StreamCallbacks = {
	// The live stream (re)connected, e.g. to catch up on a status change it does not replay
	onOpen?: () => void;
	// A status change of the run, e.g. to refetch its header
	onStatus?: (status: string) => void;
	// The run is final and every event was delivered
	onEnd?: () => void;
};

// The timeline of one run: persisted events from `GET /events`, then `GET /stream` for live events and deltas
// Deltas are not persisted, so they only drive previews that the matching persisted event replaces
export class RunStream {
	events = $state.raw<RunEvent[]>([]);
	loaded = $state(false);
	loadError = $state<unknown>(null);

	// Assistant text and reasoning of the model turn in progress
	liveText = $state('');
	liveReasoning = $state('');
	// Tool names the model announced in the turn in progress
	liveToolCalls = $state.raw<string[]>([]);
	// Live terminal output per tool call ID
	toolOutput = $state<Record<string, string>>({});
	// Whether live updates arrive: 'idle' before connecting and after the run ended, 'reconnecting' while the stream is down
	connection = $state<'idle' | 'live' | 'reconnecting'>('idle');

	#runId: string;
	#service: RunService;
	#callbacks: StreamCallbacks;
	#lastSeq = 0;
	#source: EventSource | null = null;
	#retryTimer: ReturnType<typeof setTimeout> | undefined;
	#retries = 0;
	#stopped = false;
	// Only consulted when a delta arrives, never rendered, so a plain Set is intended
	// eslint-disable-next-line svelte/prefer-svelte-reactivity
	#finishedCalls = new Set<string>();

	constructor(runId: string, callbacks: StreamCallbacks = {}, service = new RunService()) {
		this.#runId = runId;
		this.#callbacks = callbacks;
		this.#service = service;
	}

	// Loads the persisted timeline, then follows the run live when it can still change
	async start(live: boolean) {
		try {
			await this.#loadPersisted();
		} catch (e) {
			this.loadError = e;
		}
		this.loaded = true;
		if (live && !this.#stopped) this.#connect();
	}

	stop() {
		this.#stopped = true;
		this.#source?.close();
		this.#source = null;
		clearTimeout(this.#retryTimer);
		this.connection = 'idle';
	}

	async #loadPersisted() {
		while (!this.#stopped) {
			const page =
				(await this.#service.events(this.#runId, {
					after: this.#lastSeq,
					limit: EVENTS_PAGE_SIZE
				})) ?? [];
			this.#append(page);
			if (page.length < EVENTS_PAGE_SIZE) return;
		}
	}

	#connect() {
		// Resuming passes the last seen sequence, and EventSource adds Last-Event-ID itself on automatic reconnects
		const source = new EventSource(this.#service.streamUrl(this.#runId, this.#lastSeq));
		this.#source = source;

		source.addEventListener('open', () => {
			this.#retries = 0;
			this.connection = 'live';
			// The stream replays every event after the last one loaded, so it also makes up for a failed initial load
			this.loadError = null;
			this.#callbacks.onOpen?.();
		});
		source.addEventListener('event', (m) => {
			const event = parse<RunEvent>(m);
			if (event) this.#append([event]);
		});
		source.addEventListener('delta', (m) => {
			const delta = parse<RunDelta>(m);
			if (delta) this.#applyDelta(delta);
		});
		source.addEventListener('status', (m) => {
			const status = parse<{ status: string }>(m)?.status;
			if (status) this.#callbacks.onStatus?.(status);
		});
		source.addEventListener('end', () => {
			// The server closes the stream after `end`, which must not trigger EventSource's automatic reconnect
			this.stop();
			this.#clearTurn();
			this.#callbacks.onEnd?.();
		});
		source.addEventListener('error', () => {
			if (this.#stopped) return;
			// Updates stop until the stream is back, which the page says rather than looking live while frozen
			this.connection = 'reconnecting';

			// EventSource retries network errors itself but gives up for good on a failed HTTP response
			if (source.readyState !== EventSource.CLOSED) return;
			this.#source = null;
			const delay = Math.min(RECONNECT_BASE_MS * 2 ** this.#retries, RECONNECT_MAX_MS);
			this.#retries++;
			this.#retryTimer = setTimeout(() => this.#connect(), delay);
		});
	}

	// Adds events in sequence order, skipping any that a replay after a reconnect delivers twice
	#append(events: RunEvent[]) {
		const fresh = events.filter((e) => e.seq > this.#lastSeq).sort((a, b) => a.seq - b.seq);
		if (fresh.length === 0) return;

		for (const event of fresh) this.#settlePreview(event);
		this.events = [...this.events, ...fresh];
		this.#lastSeq = fresh[fresh.length - 1].seq;
	}

	// A persisted event replaces the streaming preview it corresponds to
	#settlePreview(event: RunEvent) {
		if (event.type === 'llm.call') {
			this.#clearTurn();
			return;
		}
		if (event.type !== 'tool.call' && event.type !== 'tool.result') return;
		const callId = payloadOf<ToolCallPayload>(event).callId;
		if (!callId) return;

		// Servers that don't name their tool calls get IDs like call_0 in every turn, so a new call reopens its ID
		if (event.type === 'tool.call') {
			this.#finishedCalls.delete(callId);
		} else {
			this.#finishedCalls.add(callId);
			delete this.toolOutput[callId];
		}
	}

	#applyDelta(delta: RunDelta) {
		switch (delta.type) {
			case 'text':
				this.liveText += delta.text ?? '';
				break;
			case 'reasoning':
				this.liveReasoning += delta.text ?? '';
				break;
			case 'tool_call':
				if (delta.text) this.liveToolCalls = [...this.liveToolCalls, delta.text];
				break;
			case 'tool_output': {
				// Output that arrives after the tool's result would resurrect a finished preview
				if (!delta.callId || this.#finishedCalls.has(delta.callId)) return;
				const next = (this.toolOutput[delta.callId] ?? '') + (delta.text ?? '');
				this.toolOutput[delta.callId] =
					next.length > MAX_LIVE_OUTPUT_CHARS ? next.slice(-MAX_LIVE_OUTPUT_CHARS) : next;
				break;
			}
		}
	}

	// Live tool output stays when the run ends, since it is all there is of a tool that was cut off before it reported a result
	#clearTurn() {
		this.liveText = '';
		this.liveReasoning = '';
		this.liveToolCalls = [];
	}
}

function parse<T>(message: Event): T | null {
	try {
		return JSON.parse((message as MessageEvent<string>).data) as T;
	} catch {
		return null;
	}
}
