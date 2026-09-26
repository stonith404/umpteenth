import type { Job, JobListItem, JobSpec } from '$lib/api/types';
import { describeCron } from '$lib/utils/cron-util';

// The unsubmitted description of the new-job page, kept per browser tab
// It belongs to the workspace it was written in, so switching workspaces clears it
export const NEW_JOB_DRAFT_KEY = 'umpteenth:new-job-draft';

// Explains the mode of a job with graduation turned off, next to its mode in the jobs list and the job header
export const GRADUATION_OFF_NOTE = 'Graduation is off, so every run uses the agent';

export type ConcurrencyPolicy = 'skip' | 'queue' | 'parallel';

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

export type NetworkPolicy = JobSpec['network'];

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
		sideEffects: []
	};
}

// A schedule split into the parts pages style differently, e.g. `Daily at 07:00` and a muted `UTC`
export type ScheduleParts = {
	// The schedule in plain words, e.g. `Daily at 07:00`, or the raw expression when it has no description
	label: string;
	// The city of the job's zone, e.g. `UTC`, `Zurich` or `New York`, or null when it is the viewer's own zone
	zone: string | null;
	// The raw cron expression, for tooltips
	cron: string;
	// The full IANA zone, e.g. `Europe/Zurich`, for tooltips
	timezone: string;
	// The label with the zone after a middot, e.g. `Daily at 07:00 · UTC`
	text: string;
	// The raw expression and full zone for a tooltip, e.g. `0 7 * * * · Europe/Zurich`
	detail: string;
};

type ScheduleSource = Pick<JobListItem, 'cron' | 'timezone'> & { scheduleHuman?: string | null };

// Splits a job's schedule into plain words and a zone, or returns null for jobs that only run on demand
// The words come from the compile step when it described the schedule, otherwise from the cron expression itself
export function scheduleParts(job: ScheduleSource): ScheduleParts | null {
	if (!job.cron) return null;
	const timezone = job.timezone || 'UTC';
	const label = job.scheduleHuman || describeCron(job.cron) || job.cron;

	// The zone only adds information when it differs from the one the viewer reads times in
	const zone = sameTimezone(timezone, localTimezone()) ? null : timezoneCity(timezone);
	return {
		label,
		zone,
		cron: job.cron,
		timezone,
		text: zone ? `${label} · ${zone}` : label,
		detail: `${job.cron} · ${timezone}`
	};
}

// Describes a job's schedule in plain words with its zone when that differs from the viewer's, e.g. `Daily at 07:00 · UTC`
export function scheduleLabel(job: ScheduleSource): string | null {
	return scheduleParts(job)?.text ?? null;
}

// The readable part of an IANA zone, e.g. `New York` for `America/New_York` and `UTC` for `Etc/UTC`
export function timezoneCity(timezone: string): string {
	const city = timezone.split('/').pop() || timezone;
	return city.replaceAll('_', ' ');
}

// Compares zones by their canonical names, so aliases such as `Etc/UTC` and `UTC` count as the same zone
function sameTimezone(a: string, b: string) {
	return canonicalTimezone(a) === canonicalTimezone(b);
}

function canonicalTimezone(timezone: string) {
	try {
		return new Intl.DateTimeFormat('en-US', { timeZone: timezone }).resolvedOptions().timeZone;
	} catch {
		return timezone;
	}
}

// Every IANA timezone the browser knows, for timezone pickers
export function timezones(): string[] {
	return Intl.supportedValuesOf('timeZone');
}

// The browser's own timezone, a sensible default for new schedules
export function localTimezone(): string {
	return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

// Splits a loaded job's schedule into parts, using the compiled plain-words description only while it still matches the cron expression
export function jobScheduleParts(
	job: Pick<Job, 'cron' | 'timezone' | 'spec'>
): ScheduleParts | null {
	const human = job.spec.schedule?.cron === job.cron ? job.spec.schedule?.human : null;
	return scheduleParts({ cron: job.cron, timezone: job.timezone, scheduleHuman: human });
}
