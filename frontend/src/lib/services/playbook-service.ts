import type { PlaybookUpdate, QueryOf } from '$lib/api/types';
import APIService from './api-service';

export default class PlaybookService extends APIService {
	get = (jobId: string) =>
		this.unwrap(this.api.GET('/api/jobs/{id}/playbook', { params: { path: { id: jobId } } }));

	// Every edit is stored as a new version, and a changed Dockerfile starts an image build
	update = (jobId: string, body: PlaybookUpdate) =>
		this.unwrap(this.api.PUT('/api/jobs/{id}/playbook', { params: { path: { id: jobId } }, body }));

	versions = (jobId: string, query?: QueryOf<'list-playbook-versions'>) =>
		this.unwrap(
			this.api.GET('/api/jobs/{id}/playbook/versions', {
				params: { path: { id: jobId }, query }
			})
		);

	// Includes the content of the version before, so callers can show a diff
	version = (jobId: string, version: number) =>
		this.unwrap(
			this.api.GET('/api/jobs/{id}/playbook/versions/{version}', {
				params: { path: { id: jobId, version } }
			})
		);

	// Rolling back creates a new version with the old content, so history is never rewritten
	rollback = (jobId: string, version: number) =>
		this.unwrap(
			this.api.POST('/api/jobs/{id}/playbook/rollback', {
				params: { path: { id: jobId } },
				body: { version }
			})
		);
}
