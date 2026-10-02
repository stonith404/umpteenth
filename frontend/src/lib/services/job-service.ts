import type {
	JobCompileResult,
	JobFields,
	JobListQuery,
	JobPatch,
	JobRunNow,
	JobSecret,
	JobServer,
	JobSkill,
	JobSpec,
	JobStatsRange,
	QueryOf
} from '#lib/api/types.js';
import APIService from './api-service';

export default class JobService extends APIService {
	list = (query?: JobListQuery) => this.unwrap(this.api.GET('/api/jobs', { params: { query } }));

	get = (id: string) => this.unwrap(this.api.GET('/api/jobs/{id}', { params: { path: { id } } }));

	create = (body: JobFields) => this.unwrap(this.api.POST('/api/jobs', { body }));

	// The backend merges the patch into the job: omitted fields keep their value, and an empty string clears an optional text field
	update = (id: string, body: JobPatch) =>
		this.unwrap(this.api.PATCH('/api/jobs/{id}', { params: { path: { id } }, body }));

	// The spec is saved as a whole, so a card that edits part of it starts from the stored spec instead of the one its page loaded
	// Edits another tab or member saved to the rest of the spec meanwhile then stay
	updateSpec = async (
		id: string,
		change: (spec: JobSpec) => JobSpec,
		body: Omit<JobPatch, 'spec'> = {}
	) => {
		const latest = await this.get(id);
		return this.update(id, { ...body, spec: change(latest.spec) });
	};

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/jobs/{id}', { params: { path: { id } } }));

	// Turns a plain-language description into a spec with the utility model, without saving anything
	// askQuestions is false once the user answered the questions of an earlier compile, and the signal lets the page cancel a slow compile
	compile = (
		instruction: string,
		{ askQuestions = true, signal }: { askQuestions?: boolean; signal?: AbortSignal } = {}
	): Promise<JobCompileResult> =>
		this.unwrap(
			this.api.POST('/api/jobs/compile', { body: { instruction, askQuestions }, signal })
		);

	runNow = (id: string, body: JobRunNow = {}) =>
		this.unwrap(this.api.POST('/api/jobs/{id}/runs', { params: { path: { id } }, body }));

	stats = (id: string, range?: JobStatsRange) =>
		this.unwrap(
			this.api.GET('/api/jobs/{id}/stats', { params: { path: { id }, query: { range } } })
		);

	rotateWebhookToken = (id: string) =>
		this.unwrap(this.api.POST('/api/jobs/{id}/webhook-token', { params: { path: { id } } }));

	listState = (id: string, query?: QueryOf<'list-job-state'>) =>
		this.unwrap(this.api.GET('/api/jobs/{id}/state', { params: { path: { id }, query } }));

	// baseUpdatedAt is the updatedAt of the entry the edit started from, or 0 to only add a new key
	putState = (id: string, key: string, value: string, baseUpdatedAt?: number) =>
		this.unwrap(
			this.api.PUT('/api/jobs/{id}/state/{key}', {
				params: { path: { id, key } },
				body: { value, baseUpdatedAt }
			})
		);

	deleteState = (id: string, key: string) =>
		this.unwrap(this.api.DELETE('/api/jobs/{id}/state/{key}', { params: { path: { id, key } } }));

	getMcpServers = async (id: string) =>
		(await this.unwrap(this.api.GET('/api/jobs/{id}/mcp-servers', { params: { path: { id } } }))) ??
		[];

	setMcpServers = (id: string, servers: JobServer[]) =>
		this.unwrap(
			this.api.PUT('/api/jobs/{id}/mcp-servers', { params: { path: { id } }, body: servers })
		);

	getSkills = async (id: string) =>
		(await this.unwrap(this.api.GET('/api/jobs/{id}/skills', { params: { path: { id } } }))) ?? [];

	setSkills = (id: string, skills: JobSkill[]) =>
		this.unwrap(this.api.PUT('/api/jobs/{id}/skills', { params: { path: { id } }, body: skills }));

	getSecrets = async (id: string) =>
		(await this.unwrap(this.api.GET('/api/jobs/{id}/secrets', { params: { path: { id } } }))) ?? [];

	setSecrets = (id: string, secrets: JobSecret[]) =>
		this.unwrap(
			this.api.PUT('/api/jobs/{id}/secrets', { params: { path: { id } }, body: secrets })
		);
}
