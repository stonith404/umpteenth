<script lang="ts" module>
	import type { RunStatus } from './run-meta';

	// Visual tone of each status (PLAN §13): succeeded green, failed red, live blue and animated, the rest muted
	type Tone = 'success' | 'danger' | 'live' | 'waiting' | 'muted';

	const tones: Record<RunStatus, Tone> = {
		queued: 'waiting',
		provisioning: 'live',
		running: 'live',
		verifying: 'live',
		succeeded: 'success',
		failed: 'danger',
		timed_out: 'danger',
		cancelled: 'muted',
		skipped: 'muted'
	};

	const toneClasses: Record<Tone, string> = {
		// Kumo's subtle badge tints
		success: 'bg-emerald-100/80 text-emerald-800 dark:bg-emerald-500/15 dark:text-emerald-300',
		danger: 'bg-red-100/80 text-red-700 dark:bg-red-500/15 dark:text-red-300',
		live: 'bg-blue-100/80 text-blue-800 dark:bg-blue-500/20 dark:text-blue-300',
		waiting: 'bg-neutral-200/70 text-neutral-700 dark:bg-neutral-800 dark:text-neutral-300',
		muted: 'bg-neutral-200/70 text-neutral-700 dark:bg-neutral-800 dark:text-neutral-300'
	};
</script>

<script lang="ts">
	import { cn } from '$lib/utils/style';
	import BanIcon from '@lucide/svelte/icons/ban';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import CircleDashedIcon from '@lucide/svelte/icons/circle-dashed';
	import CircleMinusIcon from '@lucide/svelte/icons/circle-minus';
	import CircleXIcon from '@lucide/svelte/icons/circle-x';
	import TimerOffIcon from '@lucide/svelte/icons/timer-off';
	import type { Component } from 'svelte';
	import { statusLabel } from './run-meta';

	type Props = {
		// One of queued|provisioning|running|verifying|succeeded|failed|cancelled|timed_out|skipped, unknown values render muted
		status: RunStatus | (string & {});
		// Shows only the icon, with the label as tooltip and accessible name, e.g. in dense lists
		iconOnly?: boolean;
		class?: string;
	};

	let { status, iconOnly = false, class: className }: Props = $props();

	const tone = $derived<Tone>(tones[status as RunStatus] ?? 'muted');
	const label = $derived(statusLabel(status));

	const icons: Partial<Record<RunStatus, Component>> = {
		queued: CircleDashedIcon,
		succeeded: CircleCheckIcon,
		failed: CircleXIcon,
		timed_out: TimerOffIcon,
		cancelled: BanIcon,
		skipped: CircleMinusIcon
	};
	const Icon = $derived(icons[status as RunStatus]);
</script>

<span
	data-slot="status-badge"
	data-status={status}
	title={iconOnly ? label : undefined}
	class={cn(
		'inline-flex h-5 w-fit shrink-0 items-center gap-1.5 rounded-full text-xs font-medium whitespace-nowrap',
		toneClasses[tone],
		iconOnly ? 'justify-center bg-transparent dark:bg-transparent' : 'px-2',
		className
	)}
>
	{#if tone === 'live'}
		<!-- Live runs pulse, so a glance at a table shows what is still moving -->
		<span class="relative flex size-2 shrink-0" aria-hidden="true">
			<span class="absolute inline-flex size-full animate-ping rounded-full bg-blue-500 opacity-60"
			></span>
			<span class="relative inline-flex size-2 rounded-full bg-blue-500"></span>
		</span>
	{:else if Icon}
		<Icon class="size-3.5 shrink-0" aria-hidden="true" />
	{/if}
	{#if iconOnly}
		<span class="sr-only">{label}</span>
	{:else}
		{label}
	{/if}
</span>
