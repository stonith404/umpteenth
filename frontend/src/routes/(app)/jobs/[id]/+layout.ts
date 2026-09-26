import JobService from '$lib/services/job-service';
import type { LayoutLoad } from './$types';

// Every tab of the job page shares the job, and saving anything invalidates `app:job` to reload it
export const load: LayoutLoad = async ({ params, fetch, depends }) => {
	depends('app:job');
	const job = await new JobService(fetch).get(params.id);
	return {
		job,
		breadcrumbLabels: { [params.id]: job.name }
	};
};
