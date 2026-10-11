import { isApiError } from '#lib/api/api-error.js';
import type { JobSuggestion } from '#lib/api/types.js';
import JobService from '#lib/services/job-service.js';
import { error } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

// Every tab of the job page shares the job, and saving anything invalidates `app:job` to reload it
export const load: LayoutLoad = async ({ params, fetch, depends }) => {
	depends('app:job');
	const service = new JobService(fetch);

	// Suggestions load next to the job, so the header counts them on every tab, and failing to load them never hides the job
	const [job, suggestions] = await Promise.all([
		service.get(params.id).catch((e: unknown) => {
			// A deleted job, or one in another workspace, gets the not-found page inside the app rather than a failure
			if (isApiError(e, 'not_found')) error(404, 'Job not found', { code: 'not_found' });
			throw e;
		}),
		service.suggestions(params.id).catch((): JobSuggestion[] => [])
	]);
	return {
		job,
		suggestions,
		breadcrumbLabels: { [params.id]: job.name }
	};
};
