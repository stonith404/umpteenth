import type { Job, JobListItem, JobSpec } from '$lib/api/types';

export const CONCURRENCY_POLICIES = ['skip', 'queue', 'parallel'] as const;
export type ConcurrencyPolicy = (typeof CONCURRENCY_POLICIES)[number];

export const concurrencyOptions: {
	value: ConcurrencyPolicy;
	label: string;
	description: string;
}[] = [
	{
		value: 'skip',
		label: 'Skip',
		description: 'A trigger while a run is active is recorded as a skipped run.'
	},
	{
		value: 'queue',
		label: 'Queue',
		description: 'A trigger while a run is active waits until that run finishes.'
	},
	{
		value: 'parallel',
		label: 'Parallel',
		description: 'Every trigger starts a run right away, even if others are active.'
	}
];

export const NETWORK_POLICIES = ['internet', 'none'] as const;
export type NetworkPolicy = (typeof NETWORK_POLICIES)[number];

// The types a job input or output can have, matching the compile step's schema
export const IO_FIELD_TYPES = [
	'string',
	'integer',
	'number',
	'boolean',
	'object',
	'array'
] as const;

// The tools the default sandbox image ships with, so users only write a Dockerfile for anything beyond them
export const DEFAULT_IMAGE_TOOLS = [
	'bash',
	'coreutils',
	'curl',
	'git',
	'jq',
	'ripgrep',
	'python3',
	'uv',
	'node',
	'npm'
];

// The API rejects `schedule: null`, so a spec without a schedule must omit the key entirely
export function cleanSpec(spec: JobSpec): JobSpec {
	const { schedule, ...rest } = spec;
	return schedule && schedule.cron.trim() ? { ...rest, schedule } : rest;
}

// An empty spec for jobs defined by hand when the compile step is unavailable
export function emptySpec(): JobSpec {
	return {
		title: '',
		goal: '',
		successCriteria: [],
		inputs: [],
		outputs: [],
		mcp: [],
		network: 'internet',
		dockerfile: null,
		sideEffects: [],
		warnings: []
	};
}

// Describes a job's schedule in plain words, falling back to the cron expression and timezone
export function scheduleLabel(
	job: Pick<JobListItem, 'cron' | 'timezone'> & { scheduleHuman?: string | null }
): string | null {
	if (!job.cron) return null;
	if (job.scheduleHuman) return job.scheduleHuman;
	return `${job.cron} (${job.timezone || 'UTC'})`;
}

// Every IANA timezone the browser knows, for timezone pickers
export function timezones(): string[] {
	try {
		return Intl.supportedValuesOf('timeZone');
	} catch {
		return ['UTC'];
	}
}

// The browser's own timezone, a sensible default for new schedules
export function localTimezone(): string {
	return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

// Describes a loaded job's schedule, using the compiled plain-words description only while it still matches the cron expression
export function jobScheduleLabel(job: Pick<Job, 'cron' | 'timezone' | 'spec'>): string | null {
	const human = job.spec.schedule?.cron === job.cron ? job.spec.schedule?.human : null;
	return scheduleLabel({ cron: job.cron, timezone: job.timezone, scheduleHuman: human });
}
