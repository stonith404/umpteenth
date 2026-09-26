<script lang="ts" module>
	import { Clock } from '$lib/utils/clock.svelte';

	// Durations of live runs tick every second, one shared clock serves every row
	export const secondClock = new Clock(1000);

	// Phase colors of the stacked mini-bar, kept apart from the mode and status colors
	export const phaseColors = {
		queue: 'bg-zinc-400 dark:bg-zinc-500',
		sandbox: 'bg-amber-500',
		llm: 'bg-fuchsia-500',
		tools: 'bg-sky-500',
		other: 'bg-zinc-300 dark:bg-zinc-600'
	} as const;
</script>

<script lang="ts">
	import type { Run } from '$lib/api/types';
	import * as Tooltip from '$lib/components/ui/tooltip';
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
		showBar = true,
		align = 'right'
	}: {
		run: RunTiming;
		showBar?: boolean;
		// Tables right-align the number in a fixed width so bars line up, headers keep it next to its label
		align?: 'left' | 'right';
	} = $props();

	const live = $derived(isLiveStatus(run.status));

	// Live runs show the time since they started, or since they were queued while they wait
	const elapsed = $derived.by(() => {
		if (run.msTotal !== null) return run.msTotal;
		if (live) return Math.max(0, secondClock.now - (run.startedAt ?? run.queuedAt));
		if (run.finishedAt && run.startedAt) return run.finishedAt - run.startedAt;
		return null;
	});

	// The bar splits queue wait plus run time into the phases the backend measured
	const phases = $derived.by(() => {
		const queue = run.msQueue ?? 0;
		const sandbox = run.msProvision ?? 0;
		const llm = run.msLlm;
		const tools = run.msTools;
		const other = Math.max(0, (run.msTotal ?? 0) - sandbox - llm - tools);
		return [
			{ key: 'queue', label: 'Queue', ms: queue },
			{ key: 'sandbox', label: 'Sandbox', ms: sandbox },
			{ key: 'llm', label: 'LLM', ms: llm },
			{ key: 'tools', label: 'Tools', ms: tools },
			{ key: 'other', label: 'Other', ms: other }
		] as const;
	});
	const phaseTotal = $derived(phases.reduce((sum, p) => sum + p.ms, 0));
</script>

{#if elapsed === null}
	<span class="text-muted-foreground">—</span>
{:else}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<span {...props} class="inline-flex cursor-default items-center gap-2">
					<span class={cn('numeric tabular-nums', align === 'right' && 'w-14 text-right')}
						>{formatDuration(elapsed)}</span
					>
					{#if showBar}
						<span
							class="bg-muted relative flex h-1.5 w-16 overflow-hidden rounded-full"
							aria-hidden="true"
						>
							{#if live}
								<span
									class="animate-duration-bar absolute inset-y-0 w-1/3 rounded-full bg-blue-500/70"
								></span>
							{:else if phaseTotal > 0}
								{#each phases as phase (phase.key)}
									{#if phase.ms > 0}
										<span
											class={cn('h-full', phaseColors[phase.key])}
											style:width="{(phase.ms / phaseTotal) * 100}%"
										></span>
									{/if}
								{/each}
							{/if}
						</span>
					{/if}
				</span>
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

<style>
	/* An indeterminate sweep for runs whose phase split is only known once they finish */
	@keyframes duration-bar {
		from {
			left: -33%;
		}
		to {
			left: 100%;
		}
	}

	.animate-duration-bar {
		animation: duration-bar 1.4s ease-in-out infinite;
	}
</style>
