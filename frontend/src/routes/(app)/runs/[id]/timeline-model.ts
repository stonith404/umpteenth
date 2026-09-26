import type { RunEvent } from '$lib/api/types';

// Payload shapes of the persisted run events (backend `internal/runner`, `internal/broker`, PLAN §11)
// The API types payloads as `unknown`, so every field is optional and read defensively

export type Usage = {
	input?: number;
	output?: number;
	cacheRead?: number;
	cacheWrite?: number;
	reasoning?: number;
};

export type LlmCallPayload = {
	turn?: number;
	model?: string;
	stop?: string;
	text?: string;
	reasoning?: string;
	toolCalls?: { id: string; name: string }[] | null;
	usage?: Usage;
	// Micro-USD
	cost?: number;
	latencyMs?: number;
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
		bytes?: number;
		logPath?: string;
	} | null;
	finish?: FinishPayload;
};

// What a scripted run's verification found, see runner.runScripted
export type VerifyPayload = {
	passed?: boolean;
	checks?: { check: string; passed: boolean; detail?: string }[];
	llm?: { pass: boolean; reason: string; summary: string };
};

export type FinishPayload = {
	status?: string;
	summary?: string;
	outputs?: Record<string, unknown> | null;
};

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

	for (const event of events) {
		const payload = (event.payload ?? {}) as Record<string, unknown>;

		switch (event.type) {
			case 'llm.call':
				steps.push({
					kind: 'llm',
					key: `e${event.seq}`,
					event,
					payload: payload as LlmCallPayload
				});
				break;

			case 'tool.call': {
				const p = payload as ToolCallPayload;
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
				const p = payload as ToolResultPayload;
				const existing =
					(p.callId && toolsByCall.get(p.callId)) ||
					(event.spanId && toolsByCall.get(event.spanId)) ||
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

	return steps;
}

// Bash results start with the exit code line the model sees, which the UI shows as a badge instead
export function stripExitCodeLine(content: string): string {
	return content.replace(/^exit code: -?\d+\n/, '');
}

// The command of a bash-like tool call, or null when the args don't carry one
export function commandOf(args: unknown): string | null {
	if (args && typeof args === 'object' && 'command' in args) {
		const command = (args as { command?: unknown }).command;
		return typeof command === 'string' ? command : null;
	}
	return null;
}

export function totalTokens(usage: Usage | undefined): number {
	if (!usage) return 0;
	return (usage.input ?? 0) + (usage.output ?? 0);
}
