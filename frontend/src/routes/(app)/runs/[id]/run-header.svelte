<script lang="ts">
	import { page } from '$app/state';
	import type { RunDetail } from '$lib/api/types';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import ModeBadge from '$lib/components/runs/mode-badge.svelte';
	import RunDuration from '$lib/components/runs/run-duration.svelte';
	import { triggerIcon, triggerLabel } from '$lib/components/runs/run-meta';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { formatMicroCost, formatTokens } from '$lib/utils/format-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CircleStopIcon from '@lucide/svelte/icons/circle-stop';
	import GraduationCapIcon from '@lucide/svelte/icons/graduation-cap';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import type { Snippet } from 'svelte';

	let {
		run,
		live,
		cost,
		tokensIn,
		tokensOut,
		turns,
		canLearn,
		onCancel,
		onRetry,
		onLearn
	}: {
		run: RunDetail;
		live: boolean;
		// Totals are passed in, since a live run's are summed from its events before the run row has them
		cost: number;
		tokensIn: number;
		tokensOut: number;
		turns: number;
		// Whether the run can be learned from right now
		canLearn: boolean;
		onCancel: () => void;
		onRetry: () => void;
		onLearn: () => void;
	} = $props();

	const TriggerIcon = $derived(triggerIcon(run.trigger));

	// The API only knows who triggered a run by user ID, which is enough to recognize the viewer
	const triggeredBy = $derived.by(() => {
		if (!run.triggeredBy) return null;
		if (run.triggeredBy === page.data.user?.id) return 'you';
		// A deleted user has no name left to show
		return run.triggeredByName ?? 'a removed user';
	});

	// A plain cancel has no more to say than the status badge already does
	const errorVisible = $derived(
		!!run.error &&
			run.status !== 'succeeded' &&
			!(run.status === 'cancelled' && run.error === 'Cancelled')
	);
</script>

{#snippet stat(label: string, value: Snippet)}
	<div class="flex min-w-0 flex-col gap-1">
		<dt class="text-muted-foreground text-xs">{label}</dt>
		<dd class="min-w-0 text-sm">{@render value()}</dd>
	</div>
{/snippet}

<header class="flex flex-col gap-5">
	<div class="flex flex-wrap items-start justify-between gap-4">
		<div class="flex min-w-0 flex-col gap-2">
			<div class="flex flex-wrap items-center gap-2">
				<StatusBadge status={run.status} />
				<ModeBadge mode={run.mode} fellBack={run.fellBack} />
				{#if run.cancelRequested && live}
					<Badge variant="outline">Cancelling…</Badge>
				{/if}
			</div>
			<h1 class="flex min-w-0 flex-wrap items-baseline gap-x-2 text-2xl font-semibold">
				<a href="/jobs/{run.jobId}" class="truncate hover:underline">{run.jobName}</a>
				<span class="text-muted-foreground numeric font-normal">#{run.number}</span>
			</h1>
			<p class="text-muted-foreground flex flex-wrap items-center gap-x-1.5 text-sm">
				{#if TriggerIcon}
					<TriggerIcon class="size-4" aria-hidden="true" />
				{/if}
				{triggerLabel(run.trigger)}
				{#if triggeredBy}
					by {triggeredBy}
				{/if}
				<span aria-hidden="true">·</span>
				{run.startedAt ? 'started' : 'queued'}
				<RelativeTime value={run.startedAt ?? run.queuedAt} />
			</p>
		</div>
		<div class="flex items-center gap-2">
			{#if live}
				<Button variant="outline" onclick={onCancel} disabled={run.cancelRequested}>
					<CircleStopIcon data-icon="inline-start" />
					Cancel
				</Button>
			{:else}
				{#if canLearn && run.reflection === 'skipped'}
					<Button variant="outline" onclick={onLearn}>
						<GraduationCapIcon data-icon="inline-start" />
						Learn from this run
					</Button>
				{/if}
				<Button variant="outline" onclick={onRetry}>
					<RotateCcwIcon data-icon="inline-start" />
					Retry
				</Button>
			{/if}
		</div>
	</div>

	<dl
		class="bg-card grid grid-cols-2 gap-x-6 gap-y-4 rounded-2xl border p-4 sm:grid-cols-4 xl:grid-cols-8"
		aria-label="Run details"
	>
		{#snippet durationValue()}
			<RunDuration {run} align="left" />
		{/snippet}
		{@render stat('Duration', durationValue)}

		{#snippet costValue()}
			<span class="numeric">{formatMicroCost(cost)}</span>
		{/snippet}
		{@render stat('Cost', costValue)}

		{#snippet tokensValue()}
			<Tooltip.Root>
				<Tooltip.Trigger>
					{#snippet child({ props })}
						<span {...props} class="numeric cursor-default">
							{formatTokens(tokensIn + tokensOut)}
						</span>
					{/snippet}
				</Tooltip.Trigger>
				<Tooltip.Content>
					<span class="numeric">
						{formatTokens(tokensIn)} in · {formatTokens(tokensOut)} out
						{#if run.tokCacheRead}· {formatTokens(run.tokCacheRead)} cache read{/if}
						{#if run.tokCacheWrite}· {formatTokens(run.tokCacheWrite)} cache write{/if}
					</span>
				</Tooltip.Content>
			</Tooltip.Root>
		{/snippet}
		{@render stat('Tokens', tokensValue)}

		{#snippet turnsValue()}
			<span class="numeric">{turns}</span>
		{/snippet}
		{@render stat('Turns', turnsValue)}

		{#snippet modelValue()}
			<span class="block truncate font-mono text-xs leading-5" title={run.modelName ?? undefined}>
				{run.modelName ?? '—'}
			</span>
		{/snippet}
		{@render stat('Model', modelValue)}

		{#snippet sandboxValue()}
			{#if run.sandboxIsolation}
				<Badge
					variant="outline"
					class="rounded-md"
					title={run.sandboxAdapter ? `${run.sandboxAdapter} adapter` : undefined}
				>
					{run.sandboxIsolation}
				</Badge>
			{:else}
				<span class="text-muted-foreground">—</span>
			{/if}
		{/snippet}
		{@render stat('Sandbox', sandboxValue)}

		{#snippet playbookValue()}
			<a href="/jobs/{run.jobId}" class="numeric hover:underline">v{run.playbookVersion}</a>
		{/snippet}
		{@render stat('Playbook', playbookValue)}

		{#snippet imageValue()}
			<span class="block truncate font-mono text-xs leading-5" title={run.imageRef ?? undefined}>
				{run.imageRef ?? '—'}
			</span>
		{/snippet}
		{@render stat('Image', imageValue)}
	</dl>

	{#if errorVisible}
		<Alert.Root variant={run.status === 'cancelled' ? 'default' : 'destructive'}>
			<CircleAlertIcon />
			<Alert.Title>
				{run.status === 'cancelled' ? 'Cancelled' : 'The run did not succeed'}
			</Alert.Title>
			<Alert.Description class="break-words whitespace-pre-wrap">{run.error}</Alert.Description>
		</Alert.Root>
	{/if}
</header>
