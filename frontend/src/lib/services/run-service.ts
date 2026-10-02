import type { QueryOf } from '#lib/api/types.js';
import APIService from './api-service';

export default class RunService extends APIService {
	list = (query?: QueryOf<'list-runs'>) =>
		this.unwrap(this.api.GET('/api/runs', { params: { query } }));

	get = (id: string) => this.unwrap(this.api.GET('/api/runs/{id}', { params: { path: { id } } }));

	// Persisted timeline events after `after`, in sequence order
	events = (id: string, query?: QueryOf<'list-run-events'>) =>
		this.unwrap(this.api.GET('/api/runs/{id}/events', { params: { path: { id }, query } }));

	cancel = (id: string) =>
		this.unwrap(this.api.POST('/api/runs/{id}/cancel', { params: { path: { id } } }));

	// Deletes a finished run with its timeline and artifacts
	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/runs/{id}', { params: { path: { id } } }));

	// Deletes the finished runs among `ids` and resolves to which ones were deleted and which were skipped
	deleteMany = (ids: string[]) => this.unwrap(this.api.POST('/api/runs/delete', { body: { ids } }));

	// Starts a new run of the same job with the same input, resolving to the new run's ID
	retry = (id: string) =>
		this.unwrap(this.api.POST('/api/runs/{id}/retry', { params: { path: { id } } }));

	// Reflects on a finished run once, whether or not its job learns automatically
	learn = (id: string) =>
		this.unwrap(this.api.POST('/api/runs/{id}/learn', { params: { path: { id } } }));

	artifacts = (id: string) =>
		this.unwrap(this.api.GET('/api/runs/{id}/artifacts', { params: { path: { id } } }));

	// Download link of one artifact, used directly as an `href` since the endpoint streams the file
	artifactUrl = (id: string, path: string) =>
		`/api/runs/${encodeURIComponent(id)}/artifact?path=${encodeURIComponent(path)}`;

	// SSE endpoint of one run's timeline
	// EventSource can't send Last-Event-ID on a fresh connection, so resuming passes the last seen sequence as `after`
	streamUrl = (id: string, after = 0) =>
		`/api/runs/${encodeURIComponent(id)}/stream${after > 0 ? `?after=${after}` : ''}`;
}
