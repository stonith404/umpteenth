import type { QueryOf } from '$lib/api/types';
import APIService from './api-service';

export default class ImageService extends APIService {
	list = (jobId: string, query?: QueryOf<'list-job-images'>) =>
		this.unwrap(this.api.GET('/api/jobs/{id}/images', { params: { path: { id: jobId }, query } }));

	// Includes the Dockerfile and the build log, which grows while the build runs
	get = (jobId: string, imageId: string) =>
		this.unwrap(
			this.api.GET('/api/jobs/{id}/images/{imageId}', {
				params: { path: { id: jobId, imageId } }
			})
		);

	// Builds the current Dockerfile again and re-resolves the base image to pick up updates
	rebuild = (jobId: string) =>
		this.unwrap(
			this.api.POST('/api/jobs/{id}/images/rebuild', { params: { path: { id: jobId } } })
		);
}
