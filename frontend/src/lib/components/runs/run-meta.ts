import type { TableFilterOption } from '$lib/components/data-table';
import CalendarClockIcon from '@lucide/svelte/icons/calendar-clock';
import CodeIcon from '@lucide/svelte/icons/code';
import CompassIcon from '@lucide/svelte/icons/compass';
import MousePointerClickIcon from '@lucide/svelte/icons/mouse-pointer-click';
import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
import ScrollTextIcon from '@lucide/svelte/icons/scroll-text';
import SparklesIcon from '@lucide/svelte/icons/sparkles';
import WebhookIcon from '@lucide/svelte/icons/webhook';
import type { Component } from 'svelte';

// UI-only vocabulary of runs (PLAN §11), the API types these fields as plain strings

// The reason the runner gives when the agent called finish with a failure, where the run's summary says why
export const AGENT_FAILURE_PATTERN = /^the agent reported (a )?failure\.?$/i;

const RUN_STATUSES = [
	'queued',
	'provisioning',
	'running',
	'verifying',
	'succeeded',
	'failed',
	'cancelled',
	'timed_out',
	'skipped'
] as const;
export type RunStatus = (typeof RUN_STATUSES)[number];

const RUN_MODES = ['explore', 'assisted', 'scripted'] as const;
export type RunMode = (typeof RUN_MODES)[number];

const RUN_TRIGGERS = ['manual', 'schedule', 'webhook', 'api', 'retry'] as const;
type RunTrigger = (typeof RUN_TRIGGERS)[number];

const statusLabels: Record<RunStatus, string> = {
	queued: 'Queued',
	provisioning: 'Provisioning',
	running: 'Running',
	verifying: 'Verifying',
	succeeded: 'Succeeded',
	failed: 'Failed',
	cancelled: 'Cancelled',
	timed_out: 'Timed out',
	skipped: 'Skipped'
};

const modeLabels: Record<RunMode, string> = {
	explore: 'Explore',
	assisted: 'Assisted',
	scripted: 'Scripted'
};

export const modeIcons: Record<RunMode, Component> = {
	explore: CompassIcon,
	assisted: SparklesIcon,
	scripted: ScrollTextIcon
};

const triggerLabels: Record<RunTrigger, string> = {
	manual: 'Manual',
	schedule: 'Schedule',
	webhook: 'Webhook',
	api: 'API',
	retry: 'Retry'
};

const triggerIcons: Record<RunTrigger, Component> = {
	manual: MousePointerClickIcon,
	schedule: CalendarClockIcon,
	webhook: WebhookIcon,
	api: CodeIcon,
	retry: RotateCcwIcon
};

// Statuses of runs that can still change, so their durations tick and they can be cancelled
const LIVE_STATUSES = new Set<string>(['queued', 'provisioning', 'running', 'verifying']);

export function isLiveStatus(status: string): boolean {
	return LIVE_STATUSES.has(status);
}

export function statusLabel(status: string): string {
	return statusLabels[status as RunStatus] ?? status;
}

export function modeLabel(mode: string): string {
	return modeLabels[mode as RunMode] ?? mode;
}

export function triggerLabel(trigger: string): string {
	return triggerLabels[trigger as RunTrigger] ?? trigger;
}

// What started a run as a phrase for the run header, e.g. 'Triggered by you', where filters and tooltips use the bare trigger labels
// `by` is who started it, for the triggers a person can pull
export function triggerPhrase(trigger: string, by?: string | null): string {
	switch (trigger) {
		case 'manual':
			return by ? `Triggered by ${by}` : 'Triggered manually';
		case 'schedule':
			return 'Triggered by the schedule';
		case 'webhook':
			return 'Triggered by a webhook';
		case 'api':
			return 'Triggered via the API';
		case 'retry':
			return by ? `Retried by ${by}` : 'Retried';
		default:
			return by ? `${triggerLabel(trigger)} by ${by}` : triggerLabel(trigger);
	}
}

export function triggerIcon(trigger: string): Component | undefined {
	return triggerIcons[trigger as RunTrigger];
}

// Options of the faceted filters in run tables
export const statusFilterOptions: TableFilterOption[] = RUN_STATUSES.map((value) => ({
	value,
	label: statusLabels[value]
}));

export const modeFilterOptions: TableFilterOption[] = RUN_MODES.map((value) => ({
	value,
	label: modeLabels[value],
	icon: modeIcons[value]
}));

export const triggerFilterOptions: TableFilterOption[] = RUN_TRIGGERS.map((value) => ({
	value,
	label: triggerLabels[value],
	icon: triggerIcons[value]
}));
