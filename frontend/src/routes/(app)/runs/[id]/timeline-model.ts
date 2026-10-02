import type { RunEvent } from '#lib/api/types.js';
import { AGENT_FAILURE_PATTERN } from '#lib/components/runs/run-meta.js';

// Payload shapes of the persisted run events (backend `internal/runner`, `internal/broker`)
// The API types payloads as `unknown`, so every field is optional and read defensively

export type Usage = {
	input?: number;
	output?: number;
	cacheRead?: number;
	cacheWrite?: number;
};

export type LlmCallPayload = {
	turn?: number;
	model?: string;
	stop?: string;
	text?: string;
	reasoning?: string;
	usage?: Usage;
	// Micro-USD
	cost?: number;
	error?: string;
};

export type ToolCallPayload = {
	callId?: string;
	name?: string;
	args?: unknown;
};

export type ToolResultPayload = {
	callId?: string;
	name?: string;
	content?: string;
	isError?: boolean;
	meta?: {
		exitCode?: number;
		timedOut?: boolean;
		oomKilled?: boolean;
	} | null;
};

// Model calls and other requests the `ump` CLI made through the broker, and first connections of its egress proxy
export type BrokerCallPayload = {
	endpoint?: string;
	ok?: boolean;
	error?: string;
	server?: string;
	tool?: string;
	isError?: boolean;
	result?: string;
	model?: string;
	usage?: Usage;
	// Micro-USD
	cost?: number;
};

export type McpCallPayload = {
	server?: string;
	transport?: string;
	tools?: number;
};

export type CompactionPayload = {
	promptTokens?: number;
	messages?: number;
	summary?: string;
	usage?: Usage;
	// Micro-USD
	cost?: number;
	error?: string;
};

// Fields of the `sandbox.*` events, each of which sets only some of them
export type SandboxPayload = {
	image?: string;
	exitCode?: number;
	output?: string;
};

// Fields of the `run.status`, `script.step`, `fallback`, `error` and `log` events, each of which sets only some of them
export type NotePayload = {
	status?: string;
	name?: string;
	reason?: string;
	message?: string;
	artifacts?: string[];
};

// What a scripted run's verification found, see runner.runScripted
export type VerifyPayload = {
	passed?: boolean;
	checks?: { check: string; passed: boolean; detail?: string }[];
	llm?: { pass: boolean; reason: string };
};

export type FinishPayload = {
	status?: string;
	summary?: string;
	outputs?: Record<string, unknown> | null;
};

// Reads an event's payload as the shape of its type, with a missing payload read as empty
export function payloadOf<T>(event: RunEvent): T {
	return (event.payload ?? {}) as T;
}

// One entry of the vertical timeline, built from one or more events
export type TimelineStep =
	| { kind: 'llm'; key: string; event: RunEvent; payload: LlmCallPayload }
	| {
			kind: 'tool';
			key: string;
			callId: string;
			name: string;
			call: RunEvent | null;
			result: RunEvent | null;
			args: unknown;
			output: ToolResultPayload | null;
	  }
	| { kind: 'image_wait'; key: string; start: RunEvent; end: RunEvent | null }
	| { kind: 'event'; key: string; event: RunEvent };

// Builds the timeline in event order, pairing tool calls with their results and image waits with their end
export function buildTimeline(events: RunEvent[]): TimelineStep[] {
	const steps: TimelineStep[] = [];
	const toolsByCall = new Map<string, Extract<TimelineStep, { kind: 'tool' }>>();
	const imageWaits = new Map<string, Extract<TimelineStep, { kind: 'image_wait' }>>();

	for (const [i, event] of events.entries()) {
		// The runner restates a failure the agent reported with finish as an error, which says nothing the finish step doesn't
		if (restatesFailedFinish(event, events[i - 1])) continue;

		switch (event.type) {
			case 'llm.call':
				steps.push({
					kind: 'llm',
					key: `e${event.seq}`,
					event,
					payload: payloadOf<LlmCallPayload>(event)
				});
				break;

			case 'tool.call': {
				const p = payloadOf<ToolCallPayload>(event);
				const callId = p.callId ?? event.spanId ?? `seq${event.seq}`;
				const step = {
					kind: 'tool' as const,
					key: `e${event.seq}`,
					callId,
					name: p.name ?? 'tool',
					call: event,
					result: null,
					args: p.args,
					output: null
				};
				toolsByCall.set(callId, step);
				if (event.spanId) toolsByCall.set(event.spanId, step);
				steps.push(step);
				break;
			}

			case 'tool.result': {
				// The span pairs a result with its call even where servers without IDs of their own reuse call IDs in every turn
				const p = payloadOf<ToolResultPayload>(event);
				const existing =
					(event.spanId && toolsByCall.get(event.spanId)) ||
					(p.callId && toolsByCall.get(p.callId)) ||
					null;
				if (existing) {
					existing.result = event;
					existing.output = p;
					break;
				}

				// A result without its call still gets a step, e.g. when the call event was lost
				steps.push({
					kind: 'tool',
					key: `e${event.seq}`,
					callId: p.callId ?? `seq${event.seq}`,
					name: p.name ?? 'tool',
					call: null,
					result: event,
					args: undefined,
					output: p
				});
				break;
			}

			case 'sandbox.image_wait': {
				const existing = event.spanId ? imageWaits.get(event.spanId) : undefined;
				if (existing) {
					existing.end = event;
					break;
				}
				const step = { kind: 'image_wait' as const, key: `e${event.seq}`, start: event, end: null };
				if (event.spanId) imageWaits.set(event.spanId, step);
				steps.push(step);
				break;
			}

			default:
				steps.push({ kind: 'event', key: `e${event.seq}`, event });
		}
	}

	return steps.filter((step) => step.kind !== 'tool' || !isSettledFinish(step.name, step.output));
}

// Bash results start with the exit code line the model sees, which the UI shows as a badge instead
export function stripExitCodeLine(content: string): string {
	return content.replace(/^exit code: -?\d+\n/, '');
}

// The command of a bash-like tool call, or null when the args don't carry one
export function commandOf(args: unknown): string | null {
	if (args && typeof args === 'object' && 'command' in args && typeof args.command === 'string') {
		return args.command;
	}
	return null;
}

// Whether an `error` event only repeats that the `finish` right before it reported a failure
function restatesFailedFinish(event: RunEvent, previous: RunEvent | undefined): boolean {
	if (event.type !== 'error' || previous?.type !== 'finish') return false;
	if (payloadOf<FinishPayload>(previous).status === 'success') return false;
	return AGENT_FAILURE_PATTERN.test(payloadOf<NotePayload>(event).message ?? '');
}

// How many turns the run has taken, which is the highest turn a model call was made for
// A call that is tried again records every attempt under its turn's number, so counting calls would count it twice
export function turnsTaken(events: RunEvent[]): number {
	let turns = 0;
	let calls = 0;
	for (const event of events) {
		if (event.type !== 'llm.call') continue;
		calls++;
		turns = Math.max(turns, payloadOf<LlmCallPayload>(event).turn ?? calls);
	}
	return turns;
}

// The number of the turn the model is streaming, which is the same turn again when its last attempt failed
export function liveTurnNumber(events: RunEvent[]): number {
	const calls = events.filter((e) => e.type === 'llm.call');
	const last = calls.at(-1);
	if (last && payloadOf<LlmCallPayload>(last).error) return turnsTaken(events);
	return turnsTaken(events) + 1;
}

// A `finish` call that went through is covered by the finish event after it, so the timeline and the waterfall leave it out
export function isSettledFinish(name: string, output: ToolResultPayload | null): boolean {
	return name === 'finish' && output?.isError !== true;
}

// How a tool reads in the UI: playbook scripts and MCP tools are namespaced with `__`, which is noise to a reader
// The raw name stays in accessible names, since that is what the model called
export type ToolDisplay = {
	// The tool's own name, e.g. `front_page` for `toolkit__front_page`
	name: string;
	// Where the tool comes from, e.g. 'Playbook script' or the MCP server's name
	source?: string;
};

export function toolDisplay(raw: string): ToolDisplay {
	const split = raw.indexOf('__');
	if (split <= 0 || split + 2 >= raw.length) return { name: raw };
	const prefix = raw.slice(0, split);
	const name = raw.slice(split + 2);
	if (prefix === 'toolkit') return { name, source: 'Playbook script' };

	// MCP tool names end in a hash that only keeps different tools apart for the model (mcp.ToolName in the backend)
	return { name: name.replace(MCP_TOOL_HASH, '') || name, source: prefix };
}

const MCP_TOOL_HASH = /__[0-9a-f]{20}$/;

// Parses a tool result that is a JSON object or array, so it can be shown pretty-printed rather than as one wrapped line
export function parseJsonResult(content: string): unknown {
	const trimmed = content.trim();
	if (!/^[[{]/.test(trimmed)) return undefined;
	try {
		return JSON.parse(trimmed);
	} catch {
		return undefined;
	}
}
