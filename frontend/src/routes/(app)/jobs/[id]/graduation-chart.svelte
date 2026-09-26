<script lang="ts" module>
	// Bars take the mode's hue from app.css, the same one the mode badges' icons use
	const modeFill: Record<string, string> = {
		explore: 'fill-mode-explore-fill',
		assisted: 'fill-mode-assisted-fill',
		scripted: 'fill-mode-scripted-fill'
	};

	const SECOND = 1000;
	const MINUTE = 60 * SECOND;
	const HOUR = 60 * MINUTE;

	// Steps the duration axis may use: 1, 2 and 5 of a unit, plus the 15, 20 and 30 a minute divides into evenly
	// Ticks then land on round times such as 0, 2m, 4m, 6m instead of 0, 1m 40s, 3m 20s
	const DURATION_STEPS_MS = [
		...[100, 200, 500],
		...[1, 2, 5, 10, 15, 20, 30].map((n) => n * SECOND),
		...[1, 2, 5, 10, 15, 20, 30].map((n) => n * MINUTE),
		...[1, 2, 3, 6, 12, 24].map((n) => n * HOUR)
	];

	// Usage steps are 1, 2 or 5 times a power of ten micro-USD or tokens, which keeps ticks at round figures like $0.05, $0.20 or 20k
	const USAGE_STEPS = Array.from({ length: 13 }, (_, power) =>
		[1, 2, 5].map((m) => m * 10 ** power)
	).flat();

	// Picks the step whose last tick sits closest above the largest value, with two to four intervals, preferring three on a tie
	// A tighter top keeps the marks tall, and a fixed step list keeps the labels round
	function niceAxis(max: number, steps: number[], maxIntervals = 4) {
		if (max <= 0) return { ticks: [0], step: 0 };
		const intervals = (step: number) => Math.max(1, Math.ceil(max / step - 1e-9));
		const fitting = steps.filter((step) => {
			const n = intervals(step);
			return n >= 2 && n <= maxIntervals;
		});
		// When no step gives two to four intervals, e.g. for a single tiny value or runs that take days, the first step with few enough intervals or whole multiples of the largest step take over
		const largest = steps[steps.length - 1];
		const candidates =
			fitting.length > 0
				? fitting
				: [
						steps.find((step) => intervals(step) <= maxIntervals) ??
							Math.ceil(max / maxIntervals / largest) * largest
					];
		const score = (step: number) => [intervals(step) * step, Math.abs(intervals(step) - 3), -step];
		const step = candidates.reduce((best, next) => {
			const [a, b] = [score(best), score(next)];
			for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return b[i] < a[i] ? next : best;
			return best;
		});
		return { ticks: Array.from({ length: intervals(step) + 1 }, (_, i) => i * step), step };
	}

	// A failed or timed-out run gets a dot in its outcome colour above its bar, so failures read as outcomes and not as another category
	const failureFill: Record<string, string> = {
		failed: 'fill-destructive',
		timed_out: 'fill-warning'
	};
</script>

<script lang="ts">
	import { goto } from '$app/navigation';
	import type { JobRunPoint, JobVersionMarker } from '$lib/api/types';
	import ModeBadge, { modeFillClasses } from '$lib/components/runs/mode-badge.svelte';
	import { modeLabel, statusLabel, type RunMode } from '$lib/components/runs/run-meta';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { formatDateTime, formatDuration } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import type { UsageFormat } from '$lib/utils/usage-util';

	let {
		runs,
		versions,
		usage
	}: {
		runs: JobRunPoint[];
		versions: JobVersionMarker[];
		// The workspace's unit, so the bars show each run's price or its tokens
		usage: UsageFormat;
	} = $props();

	// Duration ticks in the unit of the step, so a 500ms step reads 0, 500ms, 1s, 1.5s rather than rounding 1.5s up to 2s
	function durationTick(ms: number, step: number) {
		if (ms === 0) return '0';
		if (step < SECOND) return ms < SECOND ? `${ms}ms` : `${ms / SECOND}s`;
		return formatDuration(ms);
	}

	// Two panels share one x axis instead of a second y axis, so usage and duration are each read against their own scale
	const USAGE_HEIGHT = 170;
	const DURATION_HEIGHT = 100;
	// Each panel's title sits on a baseline 24px above the panel, which leaves over 12px to the tick labels above and below it and clears the version labels
	const PANEL_GAP = 52;
	const TITLE_OFFSET = 24;
	const MARGIN = { top: 40, right: 12, bottom: 26, left: 56 };
	const FAILURE_DOT_GAP = 7;
	const MAX_BAR_WIDTH = 24;
	const BAR_GAP = 2;
	const RADIUS = 4;

	let width = $state(0);
	let hovered = $state<number | null>(null);

	const plotWidth = $derived(Math.max(0, width - MARGIN.left - MARGIN.right));
	const band = $derived(runs.length > 0 ? plotWidth / runs.length : 0);
	const barWidth = $derived(Math.max(1, Math.min(MAX_BAR_WIDTH, band - BAR_GAP)));
	const usageTop = MARGIN.top;
	const durationTop = MARGIN.top + USAGE_HEIGHT + PANEL_GAP;
	const totalHeight = durationTop + DURATION_HEIGHT + MARGIN.bottom;

	const usageAxis = $derived(niceAxis(Math.max(...runs.map((r) => usage.pick(r)), 0), USAGE_STEPS));
	const durationAxis = $derived(
		niceAxis(Math.max(...runs.map((r) => r.msTotal ?? 0), 0), DURATION_STEPS_MS)
	);
	const usageMax = $derived(usageAxis.ticks[usageAxis.ticks.length - 1] || 1);
	const durationMax = $derived(durationAxis.ticks[durationAxis.ticks.length - 1] || 1);

	const modes = $derived([...new Set(runs.map((r) => r.mode))]);
	const hasFailed = $derived(runs.some((r) => r.status === 'failed'));
	const hasTimedOut = $derived(runs.some((r) => r.status === 'timed_out'));

	function bandCenter(i: number) {
		return MARGIN.left + band * i + band / 2;
	}

	function usageY(value: number) {
		return usageTop + USAGE_HEIGHT - (value / usageMax) * USAGE_HEIGHT;
	}

	function durationY(ms: number) {
		return durationTop + DURATION_HEIGHT - (ms / durationMax) * DURATION_HEIGHT;
	}

	// A column with a rounded data end and a square baseline, as one path
	function barPath(i: number, value: number) {
		const x = bandCenter(i) - barWidth / 2;
		const y = usageY(value);
		const h = usageTop + USAGE_HEIGHT - y;
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

	// Only a handful of run numbers fit under the axis, so they are spread at one even step that always ends on the latest run
	const xLabels = $derived.by(() => {
		if (runs.length === 0 || plotWidth === 0) return [];
		const last = runs.length - 1;
		const maxLabels = Math.max(2, Math.floor(plotWidth / 56));
		const minStep = Math.max(1, Math.ceil(runs.length / maxLabels));

		// A step that divides the run count evenly labels the first run as well, as long as it keeps at least half the labels
		const step =
			Array.from({ length: minStep + 1 }, (_, i) => minStep + i).find(
				(candidate) => last % candidate === 0
			) ?? minStep;

		// Counting back from the latest run keeps every gap the same, where counting up from the first would squeeze the last one
		const indexes: number[] = [];
		for (let i = last; i >= 0; i -= step) indexes.unshift(i);
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

	function openRun(i: number) {
		void goto(`/runs/${runs[i].runId}`);
	}

	// Touch screens have no hover, so the first tap on a run shows its tooltip and a second tap on it opens the run
	// The tap is handled on pointerup, which a scroll gesture cancels, so scrolling across the chart shows no tooltip
	let svgElement = $state<SVGSVGElement>();
	let previewedByTap = false;

	function onBandPointerUp(event: PointerEvent, i: number) {
		if (event.pointerType === 'mouse') return;
		previewedByTap = hovered !== i;
		hovered = i;
	}

	function onBandClick(i: number) {
		if (previewedByTap) {
			previewedByTap = false;
			return;
		}
		openRun(i);
	}

	// A tap anywhere outside the chart puts the tooltip away again
	function onDocumentPointerDown(event: PointerEvent) {
		if (event.pointerType === 'mouse' || hovered === null) return;
		if (!svgElement?.contains(event.target as Node)) hovered = null;
	}
</script>

<svelte:document onpointerdown={onDocumentPointerDown} />

<div class="flex flex-col gap-3">
	<div class="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
		{#each modes as mode (mode)}
			<span class="inline-flex items-center gap-1.5">
				<span
					class={cn(
						'size-2.5 rounded-xs',
						modeFillClasses[mode as RunMode] ?? 'bg-muted-foreground'
					)}
				></span>
				{modeLabel(mode)}
			</span>
		{/each}
		<span class="inline-flex items-center gap-1.5">
			<span class="bg-foreground/70 h-0.5 w-3.5 rounded-full"></span>
			Duration
		</span>
		{#if hasFailed}
			<span class="inline-flex items-center gap-1.5">
				<span class="bg-destructive size-2 rounded-full"></span>
				Failed
			</span>
		{/if}
		{#if hasTimedOut}
			<span class="inline-flex items-center gap-1.5">
				<span class="bg-warning size-2 rounded-full"></span>
				Timed out
			</span>
		{/if}
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
				aria-label="{usage.label} and duration per run, colored by mode"
				class="block overflow-visible"
				bind:this={svgElement}
				onpointerleave={(event) => {
					if (event.pointerType === 'mouse') hovered = null;
				}}
			>
				<!-- Gridlines and y ticks of the usage panel -->
				{#each usageAxis.ticks as tick (tick)}
					<line
						x1={MARGIN.left}
						x2={width - MARGIN.right}
						y1={usageY(tick)}
						y2={usageY(tick)}
						class="stroke-border"
						stroke-width="1"
					/>
					<text
						x={MARGIN.left - 8}
						y={usageY(tick)}
						dy="0.32em"
						text-anchor="end"
						class="fill-muted-foreground numeric text-xs"
						>{usage.formatTick(tick, usageAxis.step)}</text
					>
				{/each}
				<text x={0} y={usageTop - TITLE_OFFSET} class="fill-muted-foreground text-xs font-medium"
					>{usage.label} per run</text
				>

				<!-- Gridlines and y ticks of the duration panel -->
				{#each durationAxis.ticks as tick (tick)}
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
						class="fill-muted-foreground numeric text-xs"
						>{durationTick(tick, durationAxis.step)}</text
					>
				{/each}
				<text x={0} y={durationTop - TITLE_OFFSET} class="fill-muted-foreground text-xs font-medium"
					>Duration</text
				>

				<!-- Playbook version markers span both panels -->
				{#each versionMarkers as marker (marker.version)}
					<line
						x1={marker.x}
						x2={marker.x}
						y1={usageTop - 4}
						y2={durationTop + DURATION_HEIGHT}
						class="stroke-muted-foreground/60"
						stroke-width="1"
						stroke-dasharray="3 3"
					/>
					<text x={marker.x + 3} y={usageTop - 6} class="fill-muted-foreground numeric text-xs"
						>v{marker.version}</text
					>
				{/each}

				<!-- Hover highlight of the hovered run's band -->
				{#if hovered !== null}
					<rect
						x={MARGIN.left + band * hovered}
						y={usageTop}
						width={band}
						height={durationTop + DURATION_HEIGHT - usageTop}
						class="fill-muted-foreground/10"
					/>
				{/if}

				<!-- Usage bars, colored by the run's mode, with a dot in the outcome colour above failed and timed-out runs -->
				{#each runs as run, i (run.runId)}
					<path
						d={barPath(i, usage.pick(run))}
						class={cn(
							modeFill[run.mode] ?? 'fill-muted-foreground',
							'transition-opacity motion-reduce:transition-none',
							hovered !== null && hovered !== i && 'opacity-40'
						)}
					/>
					{#if failureFill[run.status]}
						<circle
							cx={bandCenter(i)}
							cy={usageY(usage.pick(run)) - FAILURE_DOT_GAP}
							r={Math.min(3, Math.max(1.5, barWidth / 2))}
							class={cn(
								failureFill[run.status],
								'transition-opacity motion-reduce:transition-none',
								hovered !== null && hovered !== i && 'opacity-40'
							)}
						/>
					{/if}
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
						y={durationTop + DURATION_HEIGHT + 20}
						text-anchor="middle"
						class="fill-muted-foreground numeric text-xs">#{runs[i].number}</text
					>
				{/each}

				<!-- Hit targets cover each run's whole band, bigger than the marks themselves -->
				{#each runs as run, i (run.runId)}
					<rect
						x={MARGIN.left + band * i}
						y={usageTop}
						width={band}
						height={durationTop + DURATION_HEIGHT - usageTop}
						fill="transparent"
						class="cursor-pointer"
						role="presentation"
						onpointerenter={(event) => {
							if (event.pointerType === 'mouse') hovered = i;
						}}
						onpointerup={(event) => onBandPointerUp(event, i)}
						onclick={() => onBandClick(i)}
					/>
				{/each}
			</svg>

			{#if hoveredRun}
				<div
					class="bg-popover text-popover-foreground pointer-events-none absolute top-6 left-(--tooltip-left) z-10 w-52 rounded-lg border px-3 py-2 text-xs shadow-md"
					style:--tooltip-left="{tooltipLeft}px"
				>
					<div class="mb-0.5 flex items-center justify-between gap-2">
						<span class="font-medium">Run #{hoveredRun.number}</span>
						<StatusBadge status={hoveredRun.status} appearance="plain" still />
					</div>
					<div class="text-muted-foreground mb-2">{formatDateTime(hoveredRun.queuedAt)}</div>
					<div class="grid-cols-label-figure grid gap-x-3 gap-y-1">
						<span class="text-muted-foreground">Mode</span>
						<ModeBadge mode={hoveredRun.mode} appearance="plain" class="justify-self-end" />
						<span class="text-muted-foreground">{usage.label}</span>
						<span class="numeric text-right font-medium"
							>{usage.formatBare(usage.pick(hoveredRun))}</span
						>
						<span class="text-muted-foreground">Duration</span>
						<span class="numeric text-right font-medium">
							{hoveredRun.msTotal !== null ? formatDuration(hoveredRun.msTotal) : '—'}
						</span>
						<span class="text-muted-foreground">Turns</span>
						<span class="numeric text-right font-medium">{hoveredRun.turns}</span>
						<span class="text-muted-foreground">Playbook</span>
						<!-- A run before the first version says so in muted words, like the run header does -->
						{#if hoveredRun.playbookVersion > 0}
							<span class="numeric text-right font-medium">v{hoveredRun.playbookVersion}</span>
						{:else}
							<span class="text-muted-foreground text-right">No playbook</span>
						{/if}
					</div>
				</div>
			{/if}
		{:else}
			<div class="h-(--chart-height)" style:--chart-height="{totalHeight}px"></div>
		{/if}
	</div>

	<!-- The same numbers as a table, so nothing depends on seeing the colors or hovering -->
	<!-- A table ignores the 1px width of sr-only and would widen the page on phones, so a wrapper hides it instead -->
	<div class="sr-only">
		<table>
			<caption>Runs in this period</caption>
			<thead>
				<tr>
					<th>Run</th>
					<th>Status</th>
					<th>Mode</th>
					<th>{usage.label}</th>
					<th>Duration</th>
					<th>Playbook version</th>
				</tr>
			</thead>
			<tbody>
				{#each runs as run (run.runId)}
					<tr>
						<td><a href="/runs/{run.runId}">#{run.number}</a></td>
						<td>{statusLabel(run.status)}</td>
						<td>{modeLabel(run.mode)}</td>
						<td>{usage.formatBare(usage.pick(run))}</td>
						<td>{run.msTotal !== null ? formatDuration(run.msTotal) : '—'}</td>
						<td>{run.playbookVersion}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>
