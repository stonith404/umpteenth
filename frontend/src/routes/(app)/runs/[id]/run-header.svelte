<script lang="ts">
	import { page } from '$app/state';
	import type { RunDetail } from '$lib/api/types';
	import Markdown from '$lib/components/markdown.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import ModeBadge from '$lib/components/runs/mode-badge.svelte';
	import RunDuration from '$lib/components/runs/run-duration.svelte';
	import {
		AGENT_FAILURE_PATTERN,
		statusLabel,
		triggerIcon,
		triggerPhrase
	} from '$lib/components/runs/run-meta';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import UsageAmount from '$lib/components/usage-amount.svelte';
	import { sentenceCase } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import { usageFormat } from '$lib/utils/usage-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CircleStopIcon from '@lucide/svelte/icons/circle-stop';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import GraduationCapIcon from '@lucide/svelte/icons/graduation-cap';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import TimerOffIcon from '@lucide/svelte/icons/timer-off';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { Snippet } from 'svelte';

	let {
		run,
		live,
		cost,
		tokensIn,
		tokensOut,
		turns,
		canLearn,
		showLearn = true,
		hideCancel = false,
		ref = $bindable(null),
		onCancel,
		onRetry,
		onLearn,
		onDelete
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
		// The Learned tab has its own button, so the header's is hidden while that tab is open
		showLearn?: boolean;
		// The sticky live bar shows its own Stop while the header is scrolled away, so only one is ever announced
		hideCancel?: boolean;
		// The header element, e.g. to watch whether it is scrolled out of view
		ref?: HTMLElement | null;
		onCancel: () => void;
		onRetry: () => void;
		onLearn: () => void;
		// Only passed to viewers who may delete the run
		onDelete?: () => void;
	} = $props();

	const TriggerIcon = $derived(triggerIcon(run.trigger));

	// The API only knows who triggered a run by user ID, which is enough to recognize the viewer
	const triggeredBy = $derived.by(() => {
		if (!run.triggeredBy) return null;
		if (run.triggeredBy === page.data.user?.id) return 'you';
		// A deleted user has no name left to show
		return run.triggeredByName ?? 'a removed user';
	});

	// A run without any model call has no tokens or cost to speak of, which '$0.00' would misstate as a free one
	const noModelCalls = $derived(tokensIn + tokensOut === 0 && cost === 0);

	// One usage stat, a price or tokens as the workspace chose
	const usage = $derived(usageFormat());

	// A plain cancel has no more to say than the status badge already does
	const errorVisible = $derived(
		!!run.error &&
			run.status !== 'succeeded' &&
			!(run.status === 'cancelled' && run.error === 'Cancelled')
	);

	// When the agent itself reported the failure, its summary says why, while the error only says that it did
	const agentReportedFailure = $derived(
		run.status === 'failed' && !!run.summary && AGENT_FAILURE_PATTERN.test(run.error ?? '')
	);

	// A skipped run is what the job's overlap policy asked for, so it reads as neutral as a cancelled one, like its status badge
	const alertVariant = $derived(
		run.status === 'cancelled' || run.status === 'skipped'
			? 'default'
			: run.status === 'timed_out'
				? 'warning'
				: 'destructive'
	);
</script>

{#snippet stat(label: string, value: Snippet)}
	<div class="flex min-w-0 flex-col gap-1">
		<dt class="text-muted-foreground text-sm">{label}</dt>
		<dd class="min-w-0 text-lg font-medium">{@render value()}</dd>
	</div>
{/snippet}

{#snippet dash()}
	<span class="text-muted-foreground font-normal">—</span>
{/snippet}

<header bind:this={ref} class="flex flex-col gap-5">
	<PageHeader>
		{#snippet title()}
			<!-- The non-breaking space keeps the number with the name's last word, so a wrapping title never leaves it alone on a line -->
			<!-- A deleted job has no page to go to anymore -->
			{#if run.jobDeleted}{run.jobName}{:else}<a href="/jobs/{run.jobId}" class="link-underline"
					>{run.jobName}</a
				>{/if}&nbsp;<span class="text-muted-foreground numeric font-normal">#{run.number}</span>
		{/snippet}
		{#snippet meta()}
			<!-- The status pill leads the facts under the title, so the actions line up with the title rather than with a row of badges -->
			<span class="inline-flex flex-wrap items-center gap-2">
				<StatusBadge status={run.status} />
				{#if run.cancelRequested && live}
					<Badge variant="outline">Stopping…</Badge>
				{/if}
			</span>
			<!-- The mode is a fact like the rest, the same as in the job header -->
			<ModeBadge mode={run.mode} fellBack={run.fellBack} appearance="plain" />
			<span class="inline-flex items-center gap-1.5">
				{#if TriggerIcon}
					<TriggerIcon class="size-4 shrink-0" aria-hidden="true" />
				{/if}
				{triggerPhrase(run.trigger, triggeredBy)}
			</span>
			<span class="inline-flex items-center gap-1.5">
				<ClockIcon class="size-4 shrink-0" aria-hidden="true" />
				<span>
					{run.startedAt ? 'Started' : 'Queued'}
					<RelativeTime value={run.startedAt ?? run.queuedAt} side="bottom" />
				</span>
			</span>
			{#if run.modelLabel}
				<!-- The display name reads better than the provider's model ID, which is one hover away -->
				<Tooltip.Root>
					<Tooltip.Trigger>
						{#snippet child({ props })}
							<span {...props} class="inline-flex cursor-default items-center gap-1.5">
								<CpuIcon class="size-4 shrink-0" aria-hidden="true" />
								{run.modelLabel}
							</span>
						{/snippet}
					</Tooltip.Trigger>
					<!-- Opens below, since above it would cover the title -->
					<Tooltip.Content side="bottom" align="start" mono>
						{run.modelName ?? run.modelLabel}
					</Tooltip.Content>
				</Tooltip.Root>
			{/if}
		{/snippet}
		{#snippet actions()}
			{#if live}
				<!-- Hidden but kept in place while the live bar offers Stop, so the header doesn't reflow under the bar -->
				<Button
					variant="outline"
					onclick={onCancel}
					disabled={run.cancelRequested}
					class={cn(hideCancel && 'invisible')}
				>
					<CircleStopIcon data-icon="inline-start" />
					Stop
				</Button>
			{:else}
				{#if canLearn && run.reflection === 'skipped'}
					<!-- On the Learned tab the button keeps its room from sm, where the title wraps beside it and would otherwise jump with every tab switch -->
					<!-- Phones stack the actions under the title, where the room would only push Retry aside -->
					<Button
						variant="outline"
						onclick={onLearn}
						class={cn(!showLearn && 'max-sm:hidden sm:invisible')}
					>
						<GraduationCapIcon data-icon="inline-start" />
						Learn from this run
					</Button>
				{/if}
				<!-- A retry starts the job again, which a deleted job can't be -->
				{#if !run.jobDeleted}
					<Button variant="outline" onclick={onRetry}>
						<RotateCcwIcon data-icon="inline-start" />
						Retry
					</Button>
				{/if}
				{#if onDelete}
					<!-- Learning still reads the run's timeline and adds to its usage, so the run stays until that is done -->
					<Button
						variant="destructive-outline"
						onclick={onDelete}
						disabled={run.reflection === 'pending'}
						title={run.reflection === 'pending'
							? 'The run can be deleted once learning from it is done'
							: undefined}
					>
						<Trash2Icon data-icon="inline-start" />
						Delete
					</Button>
				{/if}
			{/if}
		{/snippet}
	</PageHeader>

	<dl
		class="bg-card ring-hairline grid grid-cols-2 gap-x-6 sm:grid-cols-4 gap-y-4 rounded-lg p-4 ring-1"
		aria-label="Run details"
	>
		{#snippet durationValue()}
			<RunDuration {run} />
		{/snippet}
		{@render stat('Duration', durationValue)}

		{#snippet usageValue()}
			{#if noModelCalls}
				{@render dash()}
			{:else}
				<UsageAmount
					usage={{ cost, tokens: tokensIn + tokensOut }}
					bare
					breakdown={{
						input: tokensIn,
						output: tokensOut,
						cacheRead: run.tokCacheRead,
						cacheWrite: run.tokCacheWrite
					}}
				/>
			{/if}
		{/snippet}
		{@render stat(usage.label, usageValue)}

		{#snippet turnsValue()}
			<span class="numeric">{turns}</span>
		{/snippet}
		{@render stat('Turns', turnsValue)}

		{#snippet playbookValue()}
			{#if run.playbookVersion > 0 && run.jobDeleted}
				<span class="numeric" title="The playbook version this run started with"
					>v{run.playbookVersion}</span
				>
			{:else if run.playbookVersion > 0}
				<a
					href="/jobs/{run.jobId}/playbook"
					class="numeric link-underline"
					title="The playbook version this run started with">v{run.playbookVersion}</a
				>
			{:else}
				{@render dash()}
			{/if}
		{/snippet}
		{@render stat('Playbook', playbookValue)}
	</dl>

	{#if errorVisible}
		<Alert.Root variant={alertVariant}>
			{#if run.status === 'timed_out'}
				<TimerOffIcon />
			{:else}
				<CircleAlertIcon />
			{/if}
			{#if agentReportedFailure && run.summary}
				<Alert.Title>The agent reported a failure</Alert.Title>
				<Alert.Description class="break-words">
					<Markdown source={run.summary} />
				</Alert.Description>
			{:else}
				<Alert.Title>{statusLabel(run.status)}</Alert.Title>
				<Alert.Description class="break-words whitespace-pre-wrap"
					>{sentenceCase(run.error ?? '')}</Alert.Description
				>
			{/if}
		</Alert.Root>
	{/if}
</header>
