import { expect, type APIRequestContext } from '@playwright/test';

// One scripted answer of the fake model, see POST /api/test/llm-script
export type ScriptedResponse = {
	text?: string;
	reasoning?: string;
	toolCalls?: { name: string; args?: unknown }[];
	usage?: { input?: number; output?: number };
	delayMs?: number;
	// Why the model ended its turn, as backend/internal/llm/fake/fake.go allows
	stop?: 'end_turn' | 'tool_use' | 'max_tokens' | 'refusal';
	// Fails the call with this message instead of answering
	error?: string;
	// Turns the error into a provider API error with this HTTP status, whose status decides whether the call is repeated
	errorStatus?: number;
};

// The fields of GET /api/runs/{id} that specs check, which the items of GET /api/runs share
export type Run = {
	id: string;
	number: number;
	status: string;
	mode: string;
	trigger: string;
	triggeredBy: string | null;
	input: unknown;
	imageRef: string;
	fellBack: boolean;
	turns: number;
	cost: number;
	summary: string | null;
	// Why the run failed, was skipped or was cancelled
	error: string | null;
	startedAt: number | null;
	finishedAt: number | null;
	msTotal: number | null;
	reflection: string;
	reflectionVersion: number | null;
	reflectionError: string | null;
	reflectionOps: { op: string; status: string; test?: { status: string; detail: string } }[];
};

// One event of a run, see GET /api/runs/{id}/events
export type RunEvent = { type: string; payload: Record<string, unknown> };

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

// What a job does with a trigger that arrives while one of its runs is still active
export type ConcurrencyPolicy = 'skip' | 'queue' | 'parallel';

// The limits a job may set for itself, where every one left out falls back to the workspace's sandbox defaults
export type LimitOverrides = {
	timeoutSeconds?: number;
	maxTurns?: number;
	maxCostUsd?: number;
	cpus?: number;
	memoryMb?: number;
};

// A job's spec as POST /api/jobs takes it, where every field but the schedule is required
export type JobSpec = {
	title: string;
	goal: string;
	successCriteria: string[];
	inputs: { name: string; type: string; description?: string }[];
	outputs: { name: string; type: string; description?: string }[];
	mcp: { server: string; why: string }[];
	network: 'none' | 'internet';
	dockerfile: string | null;
	sideEffects: string[];
};

// The fields a spec may give a new job besides its name, where every one left out keeps the backend's default
export type JobOptions = {
	instruction?: string;
	selfImprove?: boolean;
	graduate?: boolean;
	concurrency?: ConcurrencyPolicy;
	cron?: string;
	timezone?: string;
	network?: 'none' | 'internet' | 'allowlist';
	limits?: LimitOverrides;
	// A model of the job's own instead of the workspace's default agent model
	modelId?: string;
	// Only the parts of the spec a test cares about, since createJob fills in the rest
	spec?: Partial<JobSpec>;
};

// The fields of GET /api/jobs/{id} that specs check, which POST /api/jobs answers with too
export type Job = {
	id: string;
	name: string;
	instruction: string;
	concurrency: ConcurrencyPolicy;
	network: string;
	allowedDomains: string[] | null;
	allowPrivateNetwork: boolean;
	runAsRoot: boolean;
	limits: Record<string, number>;
	cron: string | null;
	nextRunAt: number | null;
	spec: {
		successCriteria: string[] | null;
		sideEffects: string[] | null;
		network: string;
		schedule?: unknown;
	};
};

const TERMINAL_STATUSES = ['succeeded', 'failed', 'cancelled', 'timed_out', 'skipped'];

// Replaces the fake model's queued answers, so the next run follows exactly this script
async function scriptModel(request: APIRequestContext, responses: ScriptedResponse[]) {
	const response = await request.post('/api/test/llm-script', { data: { reset: true, responses } });
	expect(response.ok()).toBeTruthy();
}

// How many scripted answers the fake model has left, which an empty script call reports without changing it
async function pendingAnswers(request: APIRequestContext) {
	const response = await request.post('/api/test/llm-script', {
		data: { reset: false, responses: [] }
	});
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as { pending: number }).pending;
}

// Creates a job and fills in the spec fields the API requires but a test leaves out
// Learning is off by default, since reflection would take answers from the fake model's queue that the next scripted run needs
async function createJob(request: APIRequestContext, name: string, options: JobOptions = {}) {
	const { instruction = 'Do the thing.', selfImprove = false, spec, ...rest } = options;
	const fullSpec: JobSpec | undefined = spec && {
		title: name,
		goal: '',
		successCriteria: [],
		inputs: [],
		outputs: [],
		mcp: [],
		network: 'internet',
		dockerfile: null,
		sideEffects: [],
		...spec
	};
	const response = await request.post('/api/jobs', {
		data: { name, instruction, selfImprove, ...rest, spec: fullSpec }
	});
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Job;
}

// The job as GET /api/jobs/{id} returns it
async function getJob(request: APIRequestContext, jobId: string) {
	const response = await request.get(`/api/jobs/${jobId}`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Job;
}

// Starts a run of the job, with an input and instructions for this run only when given, and returns its ID together with the status the job's overlap policy gave it
async function triggerRun(
	request: APIRequestContext,
	jobId: string,
	body: { input?: unknown; instructions?: string } = {}
) {
	const response = await request.post(`/api/jobs/${jobId}/runs`, { data: body });
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { runId: string; status: string };
}

// Starts a run of the job, with an input if given, and returns its ID
async function startRun(request: APIRequestContext, jobId: string, input?: unknown) {
	return (await triggerRun(request, jobId, input === undefined ? {} : { input })).runId;
}

// The run as GET /api/runs/{id} returns it
async function getRun(request: APIRequestContext, runId: string) {
	const response = await request.get(`/api/runs/${runId}`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Run;
}

// Lists runs through the API, such as the runs of one job with a given trigger
async function listRuns(request: APIRequestContext, params: Record<string, string>) {
	const response = await request.get('/api/runs', { params });
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { items: Run[]; total: number };
}

// Every event of the run in the order they happened, including the failed attempts of a repeated model call
async function getEvents(request: APIRequestContext, runId: string) {
	const response = await request.get(`/api/runs/${runId}/events`, { params: { limit: 500 } });
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as RunEvent[];
}

// The results of the run's tool calls in the order they were made, which specs find by tool name rather than by position
async function toolResults(request: APIRequestContext, runId: string) {
	return (await getEvents(request, runId))
		.filter((e) => e.type === 'tool.result')
		.map((e) => ({
			name: (e.payload.name as string | undefined) ?? '',
			content: (e.payload.content as string | undefined) ?? ''
		}));
}

// Waits until the run has exactly the given status, such as running, so a later cancel reaches a live sandbox
async function waitForStatus(
	request: APIRequestContext,
	runId: string,
	status: string,
	timeout = 30_000
) {
	await expect
		.poll(async () => (await getRun(request, runId)).status, { timeout, intervals: [250] })
		.toBe(status);
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

// A bash loop of the given length in seconds followed by a successful finish, which keeps a run active until the loop ends or a test cancels the run
function loopScript(seconds: number, summary: string) {
	return bashThenFinish(`for i in $(seq 1 ${seconds}); do echo tick $i; sleep 1; done`, {
		status: 'success',
		summary
	});
}

// Waits until the run took the loop of loopScript from the fake model and runs it, so it stays active for the loop's length
// The run asks the model nothing while the loop runs, but its finish answer stays queued until then, and scripting the model again drops that answer
async function waitUntilLooping(request: APIRequestContext, runId: string) {
	await expect.poll(() => pendingAnswers(request), { timeout: 30_000, intervals: [250] }).toBe(1);
	await waitForStatus(request, runId, 'running');
}

// Starts a run of the job that loops for a minute and waits until it runs the loop, so a test decides when it ends by cancelling it
// From then on the run sits in its command, so a test may script answers for other runs without this one taking them
async function startBusyRun(request: APIRequestContext, jobId: string) {
	await scriptModel(request, loopScript(60, 'Should never get here'));
	const { runId } = await triggerRun(request, jobId);
	await waitUntilLooping(request, runId);
	return runId;
}

// Cancels a live run and waits until it has ended, returning its final status
async function cancelRun(request: APIRequestContext, runId: string) {
	const response = await request.post(`/api/runs/${runId}/cancel`);
	expect(response.ok()).toBeTruthy();
	return waitForRun(request, runId);
}

// Fires the job's schedule through the test-only hook, which does what the scheduler's alarm does without waiting for the cron expression to come due
// The hook answers once the job has decided about the run, so the run it created is already listed
async function fireSchedule(request: APIRequestContext, jobId: string) {
	const response = await request.post(`/api/test/jobs/${jobId}/fire-schedule`);
	expect(response.status()).toBe(204);
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
	pendingAnswers,
	createJob,
	getJob,
	triggerRun,
	startRun,
	getRun,
	listRuns,
	getEvents,
	toolResults,
	waitForStatus,
	waitForRun,
	waitForReflection,
	runScripted,
	loopScript,
	waitUntilLooping,
	startBusyRun,
	cancelRun,
	fireSchedule,
	finish,
	reflection,
	bashThenFinish
};
