import type { JobStatsRange } from '#lib/api/types.js';
import { timeRanges } from '#lib/components/runs/date-range.js';

// The periods the overview's performance stats can cover, shared by its load function and its period control
export const statsRanges = timeRanges('7d', '30d', '90d') satisfies { value: JobStatsRange }[];

// The period a link without a range shows, which is also the backend's default
export const DEFAULT_STATS_RANGE: JobStatsRange = '30d';
