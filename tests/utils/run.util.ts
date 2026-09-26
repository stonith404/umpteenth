import { expect, type APIRequestContext } from '@playwright/test';

// One scripted answer of the fake model, see POST /api/test/llm-script
export type ScriptedResponse = {
	text?: string;
	reasoning?: string;
	toolCalls?: { name: string; args?: unknown }[];
	usage?: { input?: number; output?: number };
	delayMs?: number;
};

const TERMINAL_STATUSES = ['succeeded', 'failed', 'cancelled', 'timed_out', 'skipped'];

// Replaces the fake model's queued answers, so the next run follows exactly this script
async function scriptModel(request: APIRequestContext, responses: ScriptedResponse[]) {
	const response = await request.post('/api/test/llm-script', { data: { reset: true, responses } });
	expect(response.ok()).toBeTruthy();
}

// Learning stays off, since reflection would take answers from the fake model's queue that the next scripted run needs
async function createJob(request: APIRequestContext, name: string, instruction = 'Do the thing.') {
	const response = await request.post('/api/jobs', {
		data: { name, instruction, selfImprove: false }
	});
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { id: string; name: string };
}

// Starts a run of the job and returns its ID
async function startRun(request: APIRequestContext, jobId: string) {
	const response = await request.post(`/api/jobs/${jobId}/runs`, { data: {} });
	expect(response.ok()).toBeTruthy();
	const body = (await response.json()) as { runId: string; status: string };
	return body.runId;
}

// Waits until the run reached a final status and returns it
async function waitForRun(request: APIRequestContext, runId: string, timeout = 30_000) {
	let status = '';
	await expect
		.poll(
			async () => {
				const response = await request.get(`/api/runs/${runId}`);
				status = ((await response.json()) as { status: string }).status;
				return TERMINAL_STATUSES.includes(status);
			},
			{ timeout, intervals: [250] }
		)
		.toBe(true);
	return status;
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

// A two-turn script: one bash call, then `finish` with the given outcome
function bashThenFinish(
	command: string,
	finish: { status: 'success' | 'failure'; summary: string; outputs?: Record<string, unknown> },
	delayMs = 0
): ScriptedResponse[] {
	return [
		{
			text: 'Let me check the workspace.',
			toolCalls: [{ name: 'bash', args: { command } }],
			usage: { input: 1200, output: 300 },
			delayMs
		},
		{ toolCalls: [{ name: 'finish', args: finish }], usage: { input: 1400, output: 120 } }
	];
}

export default { scriptModel, createJob, startRun, waitForRun, runScripted, bashThenFinish };
