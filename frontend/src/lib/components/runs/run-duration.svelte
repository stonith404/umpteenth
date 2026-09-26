<script lang="ts" module>
	// Legend colours of the tooltip's phases, in the step tokens of the waterfall's groups and apart from the status colours
	// Other is time no phase accounts for, drawn as a hollow dot that stays visible on the popover in both themes
	const phaseColors = {
		queue: 'bg-muted-foreground/40',
		sandbox: 'bg-step-sandbox-bar',
		llm: 'bg-step-model-bar',
		tools: 'bg-step-tool-bar',
		other: 'border-muted-foreground/60 border'
	} as const;
</script>

<script lang="ts">
	import type { Run } from '$lib/api/types';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { secondClock } from '$lib/utils/clock.svelte';
	import { formatDuration } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import { isLiveStatus } from './run-meta';

	type RunTiming = Pick<
		Run,
		| 'status'
		| 'queuedAt'
		| 'startedAt'
		| 'finishedAt'
		| 'msQueue'
		| 'msProvision'
		| 'msLlm'
		| 'msTools'
		| 'msTotal'
	>;

	let {
		run,
		plain = false,
		tabindex
	}: {
		run: RunTiming;
		// Only the number, without the tooltip, e.g. inside a line of other facts
		plain?: boolean;
		// Passed to the tooltip trigger, -1 keeps table rows from adding a tab stop per cell
		tabindex?: number;
	} = $props();

	const live = $derived(isLiveStatus(run.status));

	// Live runs show the time since they started, or since they were queued while they wait
	const elapsed = $derived.by(() => {
		if (run.msTotal !== null) return run.msTotal;
		if (live) return Math.max(0, secondClock.now - (run.startedAt ?? run.queuedAt));
		if (run.finishedAt && run.startedAt) return run.finishedAt - run.startedAt;
		return null;
	});

	// The tooltip splits queue wait plus run time into the phases the backend measured
	const phases = $derived.by(() => {
		const queue = run.msQueue ?? 0;
		const sandbox = run.msProvision ?? 0;
		const llm = run.msLlm;
		const tools = run.msTools;
		const other = Math.max(0, (run.msTotal ?? 0) - sandbox - llm - tools);
		return [
			{ key: 'queue', label: 'Queue', ms: queue },
			// The waterfall draws the same span as 'Provisioning', where 'Sandbox' names the create and destroy steps
			{ key: 'sandbox', label: 'Provisioning', ms: sandbox },
			{ key: 'llm', label: 'Model', ms: llm },
			{ key: 'tools', label: 'Tools', ms: tools },
			{ key: 'other', label: 'Other', ms: other }
		] as const;
	});
	const phaseTotal = $derived(phases.reduce((sum, p) => sum + p.ms, 0));
</script>

<!-- The number never wraps, so a narrow cell can't split '1m 20s' over two lines -->
{#if elapsed === null}
	<span class="text-muted-foreground">—</span>
{:else if plain}
	<span class="numeric whitespace-nowrap">{formatDuration(elapsed)}</span>
{:else}
	<Tooltip.Root>
		<Tooltip.Trigger {tabindex}>
			{#snippet child({ props })}
				<span {...props} class="numeric cursor-default whitespace-nowrap"
					>{formatDuration(elapsed)}</span
				>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>
			{#if live}
				<p>Still running · {formatDuration(elapsed)} so far</p>
			{:else}
				<div class="grid grid-cols-[auto_auto_auto] items-center gap-x-2 gap-y-0.5">
					{#each phases as phase (phase.key)}
						{#if phase.ms > 0}
							<span class={cn('size-2 rounded-full', phaseColors[phase.key])}></span>
							<span>{phase.label}</span>
							<span class="numeric text-right">{formatDuration(phase.ms)}</span>
						{/if}
					{/each}
					{#if phaseTotal === 0}
						<span class="col-span-3">No timing breakdown</span>
					{/if}
				</div>
			{/if}
		</Tooltip.Content>
	</Tooltip.Root>
{/if}
