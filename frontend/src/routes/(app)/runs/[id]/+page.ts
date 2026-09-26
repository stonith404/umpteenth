import RunService from '$lib/services/run-service';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params, fetch }) => {
	const run = await new RunService(fetch).get(params.id);
	return {
		run,
		breadcrumbLabels: { [params.id]: `${run.jobName} #${run.number}` }
	};
};
