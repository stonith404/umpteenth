<script lang="ts" module>
	// Mode colors (PLAN §13): Explore violet, Assisted blue, Scripted emerald, with a lighter step on the dark surface
	const modeFill: Record<string, string> = {
		explore: 'fill-violet-500 dark:fill-violet-400',
		assisted: 'fill-blue-500 dark:fill-blue-400',
		scripted: 'fill-emerald-500 dark:fill-emerald-400'
	};

	const modeSwatch: Record<string, string> = {
		explore: 'bg-violet-500 dark:bg-violet-400',
		assisted: 'bg-blue-500 dark:bg-blue-400',
		scripted: 'bg-emerald-500 dark:bg-emerald-400'
	};
</script>

<script lang="ts">
	import { goto } from '$app/navigation';
	import type { JobRunPoint, JobVersionMarker } from '$lib/api/types';
	import ModeBadge from '$lib/components/runs/mode-badge.svelte';
	import { modeLabel } from '$lib/components/runs/run-meta';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { formatDateTime, formatDuration, formatMicroCost } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';

	let { runs, versions }: { runs: JobRunPoint[]; versions: JobVersionMarker[] } = $props();

	// Two panels share one x axis instead of a second y axis, so cost and duration are each read against their own scale
	const COST_HEIGHT = 170;
	const DURATION_HEIGHT = 90;
	const PANEL_GAP = 28;
	const MARGIN = { top: 22, right: 12, bottom: 24, left: 56 };
	const MAX_BAR_WIDTH = 24;
	const BAR_GAP = 2;
	const RADIUS = 4;

	let width = $state(0);
	let hovered = $state<number | null>(null);

	const plotWidth = $derived(Math.max(0, width - MARGIN.left - MARGIN.right));
	const band = $derived(runs.length > 0 ? plotWidth / runs.length : 0);
	const barWidth = $derived(Math.max(1, Math.min(MAX_BAR_WIDTH, band - BAR_GAP)));
	const costTop = MARGIN.top;
	const durationTop = MARGIN.top + COST_HEIGHT + PANEL_GAP;
	const totalHeight = durationTop + DURATION_HEIGHT + MARGIN.bottom;

	const costTicks = $derived(niceTicks(Math.max(...runs.map((r) => r.cost), 0)));
	const durationTicks = $derived(niceTicks(Math.max(...runs.map((r) => r.msTotal ?? 0), 0)));
	const costMax = $derived(costTicks[costTicks.length - 1] || 1);
	const durationMax = $derived(durationTicks[durationTicks.length - 1] || 1);

	const modes = $derived([...new Set(runs.map((r) => r.mode))]);

	function bandCenter(i: number) {
		return MARGIN.left + band * i + band / 2;
	}

	function costY(value: number) {
		return costTop + COST_HEIGHT - (value / costMax) * COST_HEIGHT;
	}

	function durationY(ms: number) {
		return durationTop + DURATION_HEIGHT - (ms / durationMax) * DURATION_HEIGHT;
	}

	// A column with a rounded data end and a square baseline, as one path
	function barPath(i: number, value: number) {
		const x = bandCenter(i) - barWidth / 2;
		const y = costY(value);
		const h = costTop + COST_HEIGHT - y;
		if (h <= 0) return '';
		const r = Math.min(RADIUS, barWidth / 2, h);
		return `M${x},${y + h} V${y + r} Q${x},${y} ${x + r},${y} H${x + barWidth - r} Q${x + barWidth},${y} ${x + barWidth},${y + r} V${y + h} Z`;
	}

	// Runs without a measured duration (still running or never started) break the line instead of dropping to zero
	const durationSegments = $derived.by(() => {
		const segments: string[] = [];
		let current: string[] = [];
		runs.forEach((run, i) => {
			if (run.msTotal === null) {
				if (current.length > 0) segments.push(current.join(' '));
				current = [];
				return;
			}
			current.push(`${current.length === 0 ? 'M' : 'L'}${bandCenter(i)},${durationY(run.msTotal)}`);
		});
		if (current.length > 0) segments.push(current.join(' '));
		return segments;
	});

	// A version marker sits before the first run that used that version or a later one
	const versionMarkers = $derived.by(() =>
		versions
			.map((v) => {
				const index = runs.findIndex((r) => r.playbookVersion >= v.version);
				return index < 0 ? null : { ...v, x: MARGIN.left + band * index };
			})
			.filter((m): m is JobVersionMarker & { x: number } => m !== null)
	);

	// Only a handful of run numbers fit under the axis, evenly spread and always including the last run
	const xLabels = $derived.by(() => {
		if (runs.length === 0 || plotWidth === 0) return [];
		const maxLabels = Math.max(2, Math.floor(plotWidth / 56));
		const step = Math.max(1, Math.ceil(runs.length / maxLabels));
		const indexes: number[] = [];
		for (let i = 0; i < runs.length; i += step) indexes.push(i);
		if (indexes[indexes.length - 1] !== runs.length - 1) {
			if (runs.length - 1 - indexes[indexes.length - 1] < step / 2) indexes.pop();
			indexes.push(runs.length - 1);
		}
		return indexes;
	});

	const hoveredRun = $derived(hovered !== null ? runs[hovered] : null);
	// The tooltip sits beside the hovered band rather than on top of it, flipping to the left near the right edge
	const TOOLTIP_WIDTH = 208;
	const TOOLTIP_OFFSET = 12;
	const tooltipLeft = $derived.by(() => {
		if (hovered === null) return 0;
		const right = MARGIN.left + band * (hovered + 1) + TOOLTIP_OFFSET;
		if (right + TOOLTIP_WIDTH <= width) return right;
		return Math.max(0, MARGIN.left + band * hovered - TOOLTIP_OFFSET - TOOLTIP_WIDTH);
	});

	function niceTicks(max: number, count = 3): number[] {
		if (max <= 0) return [0];
		const rough = max / count;
		const magnitude = 10 ** Math.floor(Math.log10(rough));
		const step = [1, 2, 2.5, 5, 10].map((m) => m * magnitude).find((s) => s >= rough) ?? rough;
		const ticks: number[] = [];
		for (let v = 0; v <= max + step * 0.001; v += step) ticks.push(v);
		if (ticks[ticks.length - 1] < max) ticks.push(ticks[ticks.length - 1] + step);
		return ticks;
	}

	function openRun(i: number) {
		void goto(`/runs/${runs[i].runId}`);
	}
</script>

<div class="flex flex-col gap-3">
	<div class="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
		{#each modes as mode (mode)}
			<span class="inline-flex items-center gap-1.5">
				<span class={cn('size-2.5 rounded-[2px]', modeSwatch[mode] ?? 'bg-muted-foreground')}
				></span>
				{modeLabel(mode)}
			</span>
		{/each}
		<span class="inline-flex items-center gap-1.5">
			<span class="bg-foreground/70 h-0.5 w-3.5 rounded-full"></span>
			Duration
		</span>
		{#if versionMarkers.length > 0}
			<span class="inline-flex items-center gap-1.5">
				<span class="border-muted-foreground h-3 border-l border-dashed"></span>
				Playbook version
			</span>
		{/if}
	</div>

	<div class="relative" bind:clientWidth={width}>
		{#if width > 0}
			<svg
				{width}
				height={totalHeight}
				role="img"
				aria-label="Cost and duration per run, colored by mode"
				class="block overflow-visible"
				onpointerleave={() => (hovered = null)}
			>
				<!-- Gridlines and y ticks of the cost panel -->
				{#each costTicks as tick (tick)}
					<line
						x1={MARGIN.left}
						x2={width - MARGIN.right}
						y1={costY(tick)}
						y2={costY(tick)}
						class="stroke-border"
						stroke-width="1"
					/>
					<text
						x={MARGIN.left - 8}
						y={costY(tick)}
						dy="0.32em"
						text-anchor="end"
						class="fill-muted-foreground numeric text-[10px]">{formatMicroCost(tick)}</text
					>
				{/each}
				<text x={0} y={costTop - 10} class="fill-muted-foreground text-[11px] font-medium"
					>Cost per run</text
				>

				<!-- Gridlines and y ticks of the duration panel -->
				{#each durationTicks as tick (tick)}
					<line
						x1={MARGIN.left}
						x2={width - MARGIN.right}
						y1={durationY(tick)}
						y2={durationY(tick)}
						class="stroke-border"
						stroke-width="1"
					/>
					<text
						x={MARGIN.left - 8}
						y={durationY(tick)}
						dy="0.32em"
						text-anchor="end"
						class="fill-muted-foreground numeric text-[10px]"
						>{tick === 0 ? '0' : formatDuration(tick)}</text
					>
				{/each}
				<text x={0} y={durationTop - 10} class="fill-muted-foreground text-[11px] font-medium"
					>Duration</text
				>

				<!-- Playbook version markers span both panels -->
				{#each versionMarkers as marker (marker.version)}
					<line
						x1={marker.x}
						x2={marker.x}
						y1={costTop - 4}
						y2={durationTop + DURATION_HEIGHT}
						class="stroke-muted-foreground/60"
						stroke-width="1"
						stroke-dasharray="3 3"
					/>
					<text x={marker.x + 3} y={costTop - 6} class="fill-muted-foreground numeric text-[10px]"
						>v{marker.version}</text
					>
				{/each}

				<!-- Hover highlight of the hovered run's band -->
				{#if hovered !== null}
					<rect
						x={MARGIN.left + band * hovered}
						y={costTop}
						width={band}
						height={durationTop + DURATION_HEIGHT - costTop}
						class="fill-muted-foreground/10"
					/>
				{/if}

				<!-- Cost bars, colored by the run's mode -->
				{#each runs as run, i (run.runId)}
					<path
						d={barPath(i, run.cost)}
						class={cn(
							modeFill[run.mode] ?? 'fill-muted-foreground',
							'transition-opacity',
							hovered !== null && hovered !== i && 'opacity-60',
							run.status === 'failed' || run.status === 'timed_out' ? 'opacity-50' : ''
						)}
					/>
				{/each}

				<!-- Duration line with markers, surface-ringed so they stay legible on the line -->
				{#each durationSegments as segment, i (i)}
					<path
						d={segment}
						fill="none"
						class="stroke-foreground/70"
						stroke-width="2"
						stroke-linejoin="round"
						stroke-linecap="round"
					/>
				{/each}
				{#each runs as run, i (run.runId)}
					{#if run.msTotal !== null && (runs.length <= 40 || hovered === i)}
						<circle
							cx={bandCenter(i)}
							cy={durationY(run.msTotal)}
							r={hovered === i ? 5 : 3.5}
							class="fill-foreground stroke-card"
							stroke-width="2"
						/>
					{/if}
				{/each}

				<!-- Run numbers along the shared x axis -->
				{#each xLabels as i (i)}
					<text
						x={bandCenter(i)}
						y={durationTop + DURATION_HEIGHT + 16}
						text-anchor="middle"
						class="fill-muted-foreground numeric text-[10px]">#{runs[i].number}</text
					>
				{/each}

				<!-- Hit targets cover each run's whole band, bigger than the marks themselves -->
				{#each runs as run, i (run.runId)}
					<rect
						x={MARGIN.left + band * i}
						y={costTop}
						width={band}
						height={durationTop + DURATION_HEIGHT - costTop}
						fill="transparent"
						class="cursor-pointer"
						role="presentation"
						onpointerenter={() => (hovered = i)}
						onclick={() => openRun(i)}
					/>
				{/each}
			</svg>

			{#if hoveredRun}
				<div
					class="bg-popover text-popover-foreground pointer-events-none absolute top-6 z-10 w-52 rounded-xl border px-3 py-2 text-xs shadow-md"
					style:left="{tooltipLeft}px"
				>
					<div class="mb-1.5 flex items-center justify-between gap-2">
						<span class="font-medium">Run #{hoveredRun.number}</span>
						<StatusBadge status={hoveredRun.status} />
					</div>
					<div class="text-muted-foreground mb-2">{formatDateTime(hoveredRun.queuedAt)}</div>
					<div class="grid grid-cols-[1fr_auto] gap-x-3 gap-y-1">
						<span class="numeric font-semibold">{formatMicroCost(hoveredRun.cost)}</span>
						<span class="text-muted-foreground text-right">Cost</span>
						<span class="numeric font-semibold">
							{hoveredRun.msTotal !== null ? formatDuration(hoveredRun.msTotal) : '—'}
						</span>
						<span class="text-muted-foreground text-right">Duration</span>
						<span class="numeric font-semibold">{hoveredRun.turns}</span>
						<span class="text-muted-foreground text-right">Turns</span>
					</div>
					<div class="mt-2 flex items-center justify-between gap-2">
						<ModeBadge mode={hoveredRun.mode} />
						<span class="text-muted-foreground numeric">
							{hoveredRun.playbookVersion > 0
								? `Playbook v${hoveredRun.playbookVersion}`
								: 'No playbook'}
						</span>
					</div>
				</div>
			{/if}
		{:else}
			<div style:height="{totalHeight}px"></div>
		{/if}
	</div>

	<!-- The same numbers as a table, so nothing depends on seeing the colors or hovering -->
	<table class="sr-only">
		<caption>Runs in this period</caption>
		<thead>
			<tr>
				<th>Run</th>
				<th>Status</th>
				<th>Mode</th>
				<th>Cost</th>
				<th>Duration</th>
				<th>Playbook version</th>
			</tr>
		</thead>
		<tbody>
			{#each runs as run (run.runId)}
				<tr>
					<td><a href="/runs/{run.runId}">#{run.number}</a></td>
					<td>{run.status}</td>
					<td>{modeLabel(run.mode)}</td>
					<td>{formatMicroCost(run.cost)}</td>
					<td>{run.msTotal !== null ? formatDuration(run.msTotal) : '—'}</td>
					<td>{run.playbookVersion}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
