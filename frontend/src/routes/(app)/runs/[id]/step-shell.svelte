<script lang="ts" module>
	export type StepTone =
		'default' | 'llm' | 'tool' | 'sandbox' | 'success' | 'danger' | 'warning' | 'live' | 'muted';

	// Model, tool and sandbox steps use the step tokens shared with the waterfall, outcomes use the status tints
	export const toneClasses: Record<StepTone, string> = {
		default: 'bg-muted text-foreground',
		llm: 'bg-step-model-tint text-step-model',
		tool: 'bg-step-tool-tint text-step-tool',
		sandbox: 'bg-step-sandbox-tint text-step-sandbox',
		success: 'bg-success-tint text-success',
		danger: 'bg-destructive-tint text-destructive',
		warning: 'bg-warning-tint text-warning-foreground',
		live: 'bg-info-tint text-info',
		muted: 'bg-muted text-muted-foreground'
	};

	// The live tail's cells, spaced wider and wider as the signal thins out below the newest step
	const TAIL_CELLS = [0, 4, 8, 12, 17, 22, 28, 35];
</script>

<script lang="ts">
	import PixelGlyph, { type GlyphName } from '$lib/components/pixel-glyph.svelte';
	import { formatDateTime, formatDuration } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import type { Snippet } from 'svelte';

	let {
		glyph,
		title,
		tone = 'default',
		ts,
		startTs,
		compact = false,
		quiet = false,
		pulse = false,
		meta,
		children
	}: {
		glyph?: GlyphName;
		title: string | Snippet;
		tone?: StepTone;
		// When the step happened, shown as an offset from the start of the run
		ts?: number;
		startTs: number;
		// Single-line steps such as status changes take less vertical room
		compact?: boolean;
		// Milestones such as a run starting are muted, so the steps around them stand out
		quiet?: boolean;
		pulse?: boolean;
		meta?: Snippet;
		children?: Snippet;
	} = $props();
</script>

<li class={cn('relative flex gap-3.5', compact ? 'pb-3' : 'pb-5')} data-slot="timeline-step">
	<!-- The connector runs from this step's marker to the next one, stitched from cells like the mark -->
	<span
		class="connector absolute top-7 bottom-0 left-[13px] w-0.5 [li:last-child>&]:hidden"
		aria-hidden="true"
	></span>
	{#if pulse}
		<!-- Below the newest live step the signal is still arriving, so the spine trickles on as dither -->
		<svg
			class="tail text-info absolute top-8 left-[13px] hidden [li:last-child>&]:block"
			width="2"
			height="37"
			viewBox="0 0 2 37"
			aria-hidden="true"
		>
			{#each TAIL_CELLS as y, i (y)}
				<rect {y} width="2" height="2" fill="currentColor" style="--cell: {i}" />
			{/each}
		</svg>
	{/if}
	<span
		class={cn(
			'relative z-[1] flex size-7 shrink-0 items-center justify-center rounded-lg',
			toneClasses[tone]
		)}
		aria-hidden="true"
	>
		{#if glyph}
			<PixelGlyph name={glyph} class={cn(pulse && 'animate-pulse')} />
		{/if}
	</span>
	<!-- Paths, URLs and tokens in agent output often have no break opportunity, so they may break anywhere rather than widen the timeline -->
	<div class="flex min-w-0 flex-1 flex-col gap-2 pt-1 wrap-anywhere">
		<!-- The offset keeps its own column on the right, so it lines up from step to step even when the title and meta wrap -->
		<div class="grid grid-cols-[1fr_auto] items-baseline gap-x-3 text-sm">
			<div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
				<span class={quiet ? 'text-muted-foreground' : 'font-medium'}>
					{#if typeof title === 'string'}{title}{:else}{@render title()}{/if}
				</span>
				{#if meta}
					<span
						class="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-xs [&>*+*]:before:mr-2 [&>*+*]:before:content-['·']"
					>
						{@render meta()}
					</span>
				{/if}
			</div>
			{#if ts !== undefined}
				<span
					class="text-muted-foreground numeric text-xs whitespace-nowrap"
					title={formatDateTime(ts)}
				>
					+{formatDuration(Math.max(0, ts - startTs))}
				</span>
			{/if}
		</div>
		{#if children}
			{@render children()}
		{/if}
	</div>
</li>

<style>
	.connector {
		background: repeating-linear-gradient(to bottom, var(--fill) 0 2px, transparent 2px 4px);
	}

	.tail rect {
		opacity: 0.15;
		animation: trickle 1.6s linear infinite;
		animation-delay: calc(var(--cell) * 110ms);
	}

	/* Each cell lights up in turn from the top, so the tail reads as signal flowing down */
	@keyframes trickle {
		18% {
			opacity: 1;
		}
		60%,
		100% {
			opacity: 0.15;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.tail rect {
			opacity: 0.55;
			animation: none;
		}
	}
</style>
