import JobService from '$lib/services/job-service';
import type { PageLoad } from './$types';
import { DEFAULT_STATS_RANGE, statsRanges } from './stats-ranges';

// The chart's range lives in the URL, so a shared link shows the same period
// The stats reload with the job, so runs the layout hears about while the overview is open show up in them too
export const load: PageLoad = async ({ params, fetch, url, depends }) => {
	depends('app:job');
	const requested = url.searchParams.get('range');
	const range = statsRanges.find((r) => r.value === requested)?.value ?? DEFAULT_STATS_RANGE;
	const stats = await new JobService(fetch).stats(params.id, range);
	return { stats, range };
};
