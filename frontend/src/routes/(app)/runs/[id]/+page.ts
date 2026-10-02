import { isApiError } from '#lib/api/api-error.js';
import RunService from '#lib/services/run-service.js';
import { error } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params, fetch }) => {
	const run = await new RunService(fetch).get(params.id).catch((e: unknown) => {
		// A deleted run, or one in another workspace, gets the not-found page inside the app rather than a failure
		if (isApiError(e, 'not_found')) error(404, 'Run not found', { code: 'not_found' });
		throw e;
	});
	return {
		run,
		breadcrumbLabels: { [params.id]: `${run.jobName} #${run.number}` }
	};
};
