import { isApiError } from '$lib/api/api-error';
import JobService from '$lib/services/job-service';
import { error } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

// Every tab of the job page shares the job, and saving anything invalidates `app:job` to reload it
export const load: LayoutLoad = async ({ params, fetch, depends }) => {
	depends('app:job');
	const job = await new JobService(fetch).get(params.id).catch((e: unknown) => {
		// A deleted job, or one in another workspace, gets the not-found page inside the app rather than a failure
		if (isApiError(e, 'not_found')) error(404, { message: 'Job not found', code: 'not_found' });
		throw e;
	});
	return {
		job,
		breadcrumbLabels: { [params.id]: job.name }
	};
};
