import type { JobListItem, JobSpec, User } from '#lib/api/types.js';
import { describeCron } from '#lib/utils/cron-util.js';

const NEW_JOB_DRAFT_PREFIX = 'umpteenth:new-job-draft:';

// The unsubmitted description of the new-job page, kept per browser tab
// Its key names the user and the workspace it was written in, so another account signing in to the tab or a workspace switched in another tab never sees it
export function newJobDraftKey(user: Pick<User, 'id' | 'workspaceId'>) {
	return `${NEW_JOB_DRAFT_PREFIX}${user.id}:${user.workspaceId}`;
}

// Forgets every new-job draft of the tab, e.g. when signing out
export function clearNewJobDrafts() {
	try {
		for (const key of Object.keys(sessionStorage)) {
			if (key.startsWith(NEW_JOB_DRAFT_PREFIX)) sessionStorage.removeItem(key);
		}
	} catch {
		// Storage can be unavailable, e.g. in private windows, and then there is no draft either
	}
}

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

// Applies what changed between base and mine onto theirs, matching items by key, for lists the API only saves as a whole
// Items added or changed in mine win and items removed from mine go, while whatever someone else saved meanwhile stays as theirs has it
export function mergeListChanges<T>(
	base: T[],
	mine: T[],
	theirs: T[],
	key: (item: T) => string
): T[] {
	const same = (a: T, b: T) => JSON.stringify(a) === JSON.stringify(b);
	const baseByKey = new Map(base.map((item) => [key(item), item]));
	const mineByKey = new Map(mine.map((item) => [key(item), item]));
	const merged = new Map(theirs.map((item) => [key(item), item]));
	for (const k of baseByKey.keys()) {
		if (!mineByKey.has(k)) merged.delete(k);
	}
	for (const [k, item] of mineByKey) {
		const before = baseByKey.get(k);
		if (!before || !same(before, item)) merged.set(k, item);
	}
	return [...merged.values()];
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

type ScheduleSource = Pick<JobListItem, 'cron' | 'timezone'>;

// Splits a job's schedule into plain words and a zone, or returns null for jobs that only run on demand
// The words come from the cron expression itself, so they always match what the scheduler runs
export function scheduleParts(job: ScheduleSource): ScheduleParts | null {
	if (!job.cron) return null;
	const timezone = job.timezone || 'UTC';
	const label = describeCron(job.cron) || job.cron;

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

// The browser's own timezone, or undefined when it can't tell, since ICU then reports Etc/Unknown, which no timezone database knows
export function knownLocalTimezone(): string | undefined {
	const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
	if (!zone || zone === 'Etc/Unknown') return undefined;
	try {
		new Intl.DateTimeFormat('en-US', { timeZone: zone });
	} catch {
		return undefined;
	}
	return zone;
}

// The browser's own timezone, a sensible default for new schedules, falling back to UTC like a schedule without a zone
export function localTimezone(): string {
	return knownLocalTimezone() ?? 'UTC';
}

// The environment variable name a secret's name suggests, e.g. github-token becomes GITHUB_TOKEN
export function envNameOf(secretName: string) {
	return secretName
		.toUpperCase()
		.replace(/[^A-Z0-9_]/g, '_')
		.replace(/^(\d)/, '_$1');
}
