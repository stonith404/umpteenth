import type {
	JobFields,
	JobListQuery,
	JobPatch,
	JobRunNow,
	JobSecret,
	JobServer,
	JobStatsRange,
	JobSpec,
	QueryOf
} from '$lib/api/types';
import { isApiError } from '$lib/api/api-error';
import APIService from './api-service';

export default class JobService extends APIService {
	list = (query?: JobListQuery) => this.unwrap(this.api.GET('/api/jobs', { params: { query } }));

	get = (id: string) => this.unwrap(this.api.GET('/api/jobs/{id}', { params: { path: { id } } }));

	create = (body: JobFields) => this.unwrap(this.api.POST('/api/jobs', { body }));

	// The backend merges the patch into the job: omitted fields keep their value, and an empty string clears an optional text field
	update = (id: string, body: JobPatch) =>
		this.unwrap(this.api.PATCH('/api/jobs/{id}', { params: { path: { id } }, body }));

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/jobs/{id}', { params: { path: { id } } }));

	// Turns a plain-language description into a spec with the utility model, without saving anything
	// The signal lets the page cancel a slow compile
	compile = (instruction: string, signal?: AbortSignal): Promise<JobSpec> =>
		this.unwrap(this.api.POST('/api/jobs/compile', { body: { instruction }, signal }));

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

	// Search matches substrings, so an exact key is confirmed among the results
	// Reads the exact key, since a search over the list could miss it among many similar keys
	stateKeyExists = async (id: string, key: string) => {
		try {
			await this.unwrap(
				this.api.GET('/api/jobs/{id}/state/{key}', { params: { path: { id, key } } })
			);
			return true;
		} catch (error) {
			if (isApiError(error, 'not_found')) return false;
			throw error;
		}
	};

	putState = (id: string, key: string, value: string) =>
		this.unwrap(
			this.api.PUT('/api/jobs/{id}/state/{key}', { params: { path: { id, key } }, body: { value } })
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

	getSecrets = async (id: string) =>
		(await this.unwrap(this.api.GET('/api/jobs/{id}/secrets', { params: { path: { id } } }))) ?? [];

	setSecrets = (id: string, secrets: JobSecret[]) =>
		this.unwrap(
			this.api.PUT('/api/jobs/{id}/secrets', { params: { path: { id } }, body: secrets })
		);
}
