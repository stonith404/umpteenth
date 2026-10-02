import type { QueryOf } from '#lib/api/types.js';
import APIService from './api-service';

export default class StatsService extends APIService {
	// Dashboard KPIs, per-day buckets and run lists of the period, compared with the period before
	overview = (query?: QueryOf<'get-stats-overview'>) =>
		this.unwrap(this.api.GET('/api/stats/overview', { params: { query } }));
}
