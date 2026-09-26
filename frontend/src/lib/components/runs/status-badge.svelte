<script lang="ts" module>
	import type { BadgeVariant } from '$lib/components/ui/badge';
	import type { RunStatus } from './run-meta';

	// Colour means outcome only: succeeded green, failed red, timed out amber, live blue, everything else neutral
	export type StatusTone = 'success' | 'danger' | 'warning' | 'live' | 'neutral';

	const tones: Record<RunStatus, StatusTone> = {
		queued: 'neutral',
		provisioning: 'live',
		running: 'live',
		verifying: 'live',
		succeeded: 'success',
		failed: 'danger',
		timed_out: 'warning',
		cancelled: 'neutral',
		skipped: 'neutral'
	};

	// Token classes per tone, shared with other status displays such as the jobs list's run history strip
	export const statusToneClasses: Record<
		StatusTone,
		{ badge: BadgeVariant; icon: string; fill: string; label: string }
	> = {
		success: {
			badge: 'success',
			icon: 'text-success',
			fill: 'bg-success',
			label: 'text-foreground'
		},
		danger: {
			badge: 'destructive',
			icon: 'text-destructive',
			fill: 'bg-destructive',
			label: 'text-destructive'
		},
		warning: {
			badge: 'warning',
			icon: 'text-warning-foreground',
			fill: 'bg-warning',
			label: 'text-warning-foreground'
		},
		live: { badge: 'info', icon: 'text-info', fill: 'bg-info', label: 'text-foreground' },
		neutral: {
			badge: 'secondary',
			icon: 'text-muted-foreground',
			fill: 'bg-muted-foreground/40',
			label: 'text-foreground'
		}
	};

	// The tone of a run status, unknown values are neutral
	export function statusTone(status: string): StatusTone {
		return tones[status as RunStatus] ?? 'neutral';
	}
</script>

<script lang="ts">
	import { badgeVariants } from '$lib/components/ui/badge';
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
		// One of queued|provisioning|running|verifying|succeeded|failed|cancelled|timed_out|skipped, unknown values render neutral
		status: RunStatus | (string & {});
		// 'pill' is a tinted badge for detail headers, 'plain' is the tinted icon and a label without a background for tables and lists
		appearance?: 'pill' | 'plain';
		// Shows only the icon, with the label as tooltip and screen reader text, e.g. in dense lists and on phones
		iconOnly?: boolean;
		// Stops the live pulse, for statuses that are history such as the steps of a finished run's timeline
		still?: boolean;
		class?: string;
	};

	let {
		status,
		appearance = 'pill',
		iconOnly = false,
		still = false,
		class: className
	}: Props = $props();

	const tone = $derived(statusTone(status));
	const toneClasses = $derived(statusToneClasses[tone]);
	const label = $derived(statusLabel(status));
	const pill = $derived(appearance === 'pill' && !iconOnly);

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
	data-appearance={iconOnly ? 'icon' : appearance}
	title={iconOnly ? label : undefined}
	class={cn(
		pill
			? [badgeVariants({ variant: toneClasses.badge }), 'gap-1.5 [&>svg]:size-3.5!']
			: 'inline-flex w-fit shrink-0 items-center gap-1.5 whitespace-nowrap',
		iconOnly && 'size-5 justify-center',
		className
	)}
>
	{#if tone === 'live'}
		<!-- Live runs pulse, so a glance at a table shows what is still moving -->
		<span
			class={cn('relative flex shrink-0 items-center justify-center', pill ? 'size-3.5' : 'size-4')}
			aria-hidden="true"
		>
			{#if !still}
				<span class="bg-info absolute inline-flex size-2 animate-ping rounded-full opacity-60"
				></span>
			{/if}
			<span class="bg-info relative inline-flex size-2 rounded-full"></span>
		</span>
	{:else if Icon}
		<Icon
			class={cn('shrink-0', pill ? 'size-3.5' : ['size-4', toneClasses.icon])}
			aria-hidden="true"
		/>
	{/if}
	{#if iconOnly}
		<span class="sr-only">{label}</span>
	{:else if pill}
		{label}
	{:else}
		<span class={toneClasses.label}>{label}</span>
	{/if}
</span>
