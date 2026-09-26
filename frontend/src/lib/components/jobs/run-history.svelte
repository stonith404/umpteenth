<script lang="ts" module>
	// How many runs a strip shows, which is also how many the job list returns per job
	export const RUN_HISTORY_SLOTS = 10;
</script>

<!--
@component
A job's latest runs as a strip of small blocks in their status colours, oldest on the left, each one a link to its run with a tooltip.
Slots without a run stay as faint blocks, so strips of different jobs line up in a table.
The strip is a single tab stop on the newest run, and the arrow keys move between the runs.
Example: `<RunHistory runs={job.recentRuns} />`
-->
<script lang="ts">
	import type { JobRecentRun } from '$lib/api/types';
	import { statusLabel } from '$lib/components/runs/run-meta';
	import { statusTone, statusToneClasses } from '$lib/components/runs/status-badge.svelte';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { formatShortDate, formatTime } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import { Tooltip as TooltipPrimitive } from 'bits-ui';

	type Props = {
		// The job's latest runs, newest first as the API returns them
		runs: JobRecentRun[] | null | undefined;
		// The number of slots, so strips of jobs with fewer runs keep the same width
		slots?: number;
		class?: string;
	};

	let { runs, slots = RUN_HISTORY_SLOTS, class: className }: Props = $props();

	// Oldest first, so the strip reads left to right like a timeline and the newest run sits next to the time after it
	const ordered = $derived((runs ?? []).slice(0, slots).reverse());
	const emptySlots = $derived(Math.max(0, slots - ordered.length));

	// The run that holds the strip's tab stop, the newest one until the user moves to another
	let activeId = $state<string | null>(null);
	const tabStopId = $derived(
		ordered.some((run) => run.id === activeId) ? activeId : (ordered.at(-1)?.id ?? null)
	);

	// One tooltip serves the whole strip, so it slides from block to block instead of closing and reopening
	const tether = TooltipPrimitive.createTether<JobRecentRun>();

	// Live runs pulse like the live status badge, finished ones are a solid block in their outcome colour
	function blockClass(run: JobRecentRun) {
		const tone = statusTone(run.status);
		return cn(statusToneClasses[tone].fill, tone === 'live' && 'animate-pulse');
	}

	// Short enough for a tooltip, the year only shows for runs from another year
	function runTime(run: JobRecentRun) {
		return `${formatShortDate(run.queuedAt)}, ${formatTime(run.queuedAt)}`;
	}

	function runText(run: JobRecentRun) {
		return `#${run.number} · ${statusLabel(run.status)} · ${runTime(run)}`;
	}

	// Arrow keys, Home and End move the focus along the strip like in a toolbar
	function onKeydown(event: KeyboardEvent, index: number) {
		const last = ordered.length - 1;
		const next: Record<string, number> = {
			ArrowLeft: Math.max(0, index - 1),
			ArrowRight: Math.min(last, index + 1),
			Home: 0,
			End: last
		};
		const target = next[event.key];
		if (target === undefined) return;
		event.preventDefault();
		const list = (event.currentTarget as HTMLElement).closest('ol');
		list?.querySelectorAll<HTMLAnchorElement>('a[data-run-id]')[target]?.focus();
	}

	// bits-ui gives every trigger the attributes of a button, which mean nothing on a link
	function linkProps(props: Record<string, unknown>) {
		return { ...props, type: undefined, disabled: undefined };
	}
</script>

<Tooltip.Root {tether}>
	{#snippet children({ payload })}
		<!-- Each link is 2px wider than its block and taller than it, so the small blocks are easier to hit while the visible gap stays 2px -->
		<ol
			aria-label="Recent runs"
			class={cn(
				'-mx-px flex shrink-0 items-center',
				// While one run is pointed at or focused, the others step back so it stands out
				'[&:has(a:focus-visible)>li:not(:has(a:focus-visible))]:opacity-35 [&:has(a:hover)>li:not(:has(a:hover))]:opacity-35',
				className
			)}
		>
			{#each { length: emptySlots }, i (i)}
				<li class="flex h-6 px-px transition-opacity duration-150" aria-hidden="true">
					<span class="bg-hairline my-auto h-4 w-1.5 rounded-xs"></span>
				</li>
			{/each}
			{#each ordered as run, i (run.id)}
				<li class="flex transition-opacity duration-150">
					<!-- Handlers go on the trigger, which chains them with its own focus and pointer handlers -->
					<Tooltip.Trigger
						{tether}
						payload={run}
						data-run-id={run.id}
						tabindex={run.id === tabStopId ? 0 : -1}
						onfocus={() => (activeId = run.id)}
						onkeydown={(event: KeyboardEvent) => onKeydown(event, i)}
					>
						{#snippet child({ props })}
							<a
								{...linkProps(props)}
								href="/runs/{run.id}"
								class="group/run flex h-6 px-px outline-none"
							>
								<span
									class={cn(
										'my-auto h-4 w-1.5 rounded-xs',
										'group-focus-visible/run:outline-ring group-focus-visible/run:outline-2 group-focus-visible/run:outline-offset-1',
										blockClass(run)
									)}
								></span>
								<span class="sr-only">{runText(run)}</span>
							</a>
						{/snippet}
					</Tooltip.Trigger>
				</li>
			{/each}
		</ol>
		{#if payload}
			<Tooltip.Content>
				<span class={cn('h-3 w-1.5 shrink-0 rounded-xs', blockClass(payload))} aria-hidden="true"
				></span>
				<span class="numeric whitespace-nowrap">
					<span class="font-medium">#{payload.number}</span>
					<span class="text-muted-foreground">·</span>
					{statusLabel(payload.status)}
					<span class="text-muted-foreground">· {runTime(payload)}</span>
				</span>
			</Tooltip.Content>
		{/if}
	{/snippet}
</Tooltip.Root>
