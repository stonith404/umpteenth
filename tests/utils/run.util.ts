import { expect, type APIRequestContext } from '@playwright/test';

// One scripted answer of the fake model, see POST /api/test/llm-script
export type ScriptedResponse = {
	text?: string;
	reasoning?: string;
	toolCalls?: { name: string; args?: unknown }[];
	usage?: { input?: number; output?: number };
	delayMs?: number;
};

// The fields of GET /api/runs/{id} that specs check
export type Run = {
	status: string;
	mode: string;
	trigger: string;
	input: unknown;
	imageRef: string;
	fellBack: boolean;
	turns: number;
	cost: number;
	summary: string | null;
	reflection: string;
	reflectionVersion: number | null;
	reflectionOps: { op: string; status: string; test?: { status: string; detail: string } }[];
};

// One playbook change in a reflection answer, whose other fields default to null
export type ReflectionOp = {
	op: string;
	rationale?: string;
	id?: string;
	kind?: string;
	text?: string;
	name?: string;
	content?: string | null;
};

const TERMINAL_STATUSES = ['succeeded', 'failed', 'cancelled', 'timed_out', 'skipped'];

// Replaces the fake model's queued answers, so the next run follows exactly this script
async function scriptModel(request: APIRequestContext, responses: ScriptedResponse[]) {
	const response = await request.post('/api/test/llm-script', { data: { reset: true, responses } });
	expect(response.ok()).toBeTruthy();
}

// Learning is off by default, since reflection would take answers from the fake model's queue that the next scripted run needs
async function createJob(
	request: APIRequestContext,
	name: string,
	{ instruction = 'Do the thing.', selfImprove = false } = {}
) {
	const response = await request.post('/api/jobs', {
		data: { name, instruction, selfImprove }
	});
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { id: string; name: string };
}

// Starts a run of the job, with an input if given, and returns its ID
async function startRun(request: APIRequestContext, jobId: string, input?: unknown) {
	const response = await request.post(`/api/jobs/${jobId}/runs`, {
		data: input === undefined ? {} : { input }
	});
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as { runId: string }).runId;
}

async function getRun(request: APIRequestContext, runId: string) {
	const response = await request.get(`/api/runs/${runId}`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Run;
}

// Waits until the run reached a final status and returns it
async function waitForRun(request: APIRequestContext, runId: string, timeout = 30_000) {
	let status = '';
	await expect
		.poll(
			async () => {
				status = (await getRun(request, runId)).status;
				return TERMINAL_STATUSES.includes(status);
			},
			{ timeout, intervals: [250] }
		)
		.toBe(true);
	return status;
}

// Waits until reflection on a finished run ended and returns the run
// A run's final status and whether reflection follows are stored together, so a finished run is never pending by mistake
async function waitForReflection(request: APIRequestContext, runId: string) {
	let run: Run | undefined;
	await expect
		.poll(
			async () => {
				run = await getRun(request, runId);
				return run.reflection;
			},
			{ timeout: 30_000, intervals: [250] }
		)
		.not.toBe('pending');
	return run!;
}

// Scripts the model, runs the job and waits for the result, since runs sharing the fake model must not overlap
async function runScripted(
	request: APIRequestContext,
	jobId: string,
	responses: ScriptedResponse[]
) {
	await scriptModel(request, responses);
	const runId = await startRun(request, jobId);
	const status = await waitForRun(request, runId);
	return { runId, status };
}

// The agent's finish call for a successful run
function finish(summary: string, outputs?: Record<string, unknown>): ScriptedResponse {
	return { toolCalls: [{ name: 'finish', args: { status: 'success', summary, outputs } }] };
}

// One structured reflection answer, which the fake model returns as native JSON output
function reflection(
	summary: string,
	ops: ReflectionOp[] = [],
	usedLearnings: string[] = []
): ScriptedResponse {
	const full = ops.map((op) => ({
		rationale: 'It saves the next run work.',
		id: null,
		kind: null,
		text: null,
		when: null,
		name: null,
		content: null,
		...op
	}));
	return { text: JSON.stringify({ summary, usedLearnings, ops: full }) };
}

// A two-turn script: one bash call, then `finish` with the given outcome
function bashThenFinish(
	command: string,
	finish: { status: 'success' | 'failure'; summary: string; outputs?: Record<string, unknown> }
): ScriptedResponse[] {
	return [
		{
			text: 'Let me check the workspace.',
			toolCalls: [{ name: 'bash', args: { command } }],
			usage: { input: 1200, output: 300 }
		},
		{ toolCalls: [{ name: 'finish', args: finish }], usage: { input: 1400, output: 120 } }
	];
}

export default {
	scriptModel,
	createJob,
	startRun,
	getRun,
	waitForRun,
	waitForReflection,
	runScripted,
	finish,
	reflection,
	bashThenFinish
};
