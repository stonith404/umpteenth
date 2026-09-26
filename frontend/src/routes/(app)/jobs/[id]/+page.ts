import type { JobStatsRange } from '$lib/api/types';
import JobService from '$lib/services/job-service';
import type { PageLoad } from './$types';

const RANGES: JobStatsRange[] = ['7d', '30d', '90d'];

// The chart's range lives in the URL, so a shared link shows the same period
export const load: PageLoad = async ({ params, fetch, url, depends }) => {
	depends('app:job');
	const requested = url.searchParams.get('range') as JobStatsRange | null;
	const range = requested && RANGES.includes(requested) ? requested : '30d';
	const stats = await new JobService(fetch).stats(params.id, range);
	return { stats, range };
};
