<script lang="ts">
	import PixelGlyph, { type GlyphName } from '$lib/components/pixel-glyph.svelte';
	import * as Card from '$lib/components/ui/card';
	import { Clock } from '$lib/utils/clock.svelte';
	import { cn } from '$lib/utils/style';
	import { cubicOut } from 'svelte/easing';
	import { MediaQuery } from 'svelte/reactivity';
	import type { TransitionConfig } from 'svelte/transition';
	import { toneClasses } from '../../runs/[id]/step-shell.svelte';

	let { startedAt }: { startedAt: number } = $props();

	// Compiling takes 10-60 seconds, so the steps advance on a timer to show that work is happening
	// They describe what the utility model produces, the backend reports no real progress
	const steps: { label: string; at: number; glyph: GlyphName }[] = [
		{ label: 'Reading your description', at: 0, glyph: 'prompt' },
		{ label: 'Working out the schedule', at: 3, glyph: 'hourglass' },
		{ label: 'Writing success criteria', at: 7, glyph: 'flag' },
		{ label: 'Matching MCP servers', at: 12, glyph: 'plug' },
		{ label: 'Checking the environment', at: 18, glyph: 'crate' },
		{ label: 'Looking for open questions', at: 26, glyph: 'list' }
	];

	// Enough cells to fill the connector below a step whose title wraps onto a second line
	const FLOW_CELLS = 10;

	const clock = new Clock(250);
	const elapsed = $derived(Math.max(0, (clock.now - startedAt) / 1000));
	const current = $derived(steps.findLastIndex((step) => elapsed >= step.at));

	// Tailwind's xl, where the rail sits beside the description instead of above it
	const wide = new MediaQuery('min-width: 80rem');

	// Grows the rail along the axis it takes room from, so the description card gives up its width or moves down smoothly instead of jumping
	// The card inside has a fixed width from xl, so its text never reflows while the rail opens and is only revealed by the clip
	function openRail(node: HTMLElement, { duration = 300 } = {}): TransitionConfig {
		const style = getComputedStyle(node);
		const [dimension, margin] = wide.current
			? (['width', 'margin-left'] as const)
			: (['height', 'margin-bottom'] as const);
		const size = parseFloat(style.getPropertyValue(dimension));
		const gap = parseFloat(style.getPropertyValue(margin));

		// The clip margin keeps the card's outer ring visible, which a plain overflow clip would cut off at the edges
		return {
			duration,
			easing: cubicOut,
			css: (t) =>
				`overflow: clip; overflow-clip-margin: 1px; ${dimension}: ${t * size}px; ${margin}: ${t * gap}px; opacity: ${t};`
		};
	}
</script>

<!-- Beside the description from xl and above it below, where the progress is the first thing seen and read out -->
<div class="shrink-0 max-xl:mb-6 xl:order-last xl:ml-6" transition:openRail>
	<Card.Root class="xl:w-72">
		<Card.Header>
			<Card.Title>Compiling your job</Card.Title>
			<Card.Description>
				The utility model turns your description into a spec you can review.
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<!-- Only the step is announced, so a screen reader hears each one once as it starts -->
			<p class="sr-only" aria-live="polite">{steps[current].label}</p>
			<!-- Marked like a run's timeline: done steps turn into a success check, the current one pulses and pending ones wait as rings -->
			<ol class="flex flex-col" aria-hidden="true">
				{#each steps as step, i (step.label)}
					{@const done = i < current}
					{@const active = i === current}
					<li class="relative flex gap-3 pb-4 last:pb-0">
						{#if i < steps.length - 1}
							{#if active}
								<!-- The signal trickles down from the current step towards the next, like the tail below a live run's newest step -->
								<span
									class="text-info absolute top-8 bottom-0.5 left-[13px] flex w-0.5 flex-col gap-0.5 overflow-hidden"
								>
									{#each { length: FLOW_CELLS }, cell (cell)}
										<span class="flow-cell size-0.5 shrink-0 bg-current" style="--cell: {cell}"
										></span>
									{/each}
								</span>
							{:else}
								<span
									class={cn('connector absolute top-7 bottom-0 left-[13px] w-0.5', done && 'done')}
								></span>
							{/if}
						{/if}
						<span
							class={cn(
								'relative z-[1] flex size-7 shrink-0 items-center justify-center rounded-lg transition-colors duration-500',
								toneClasses[done ? 'success' : active ? 'live' : 'muted']
							)}
						>
							<PixelGlyph
								name={done ? 'check' : active ? step.glyph : 'ring'}
								class={cn(active && 'animate-pulse', !done && !active && 'opacity-60')}
							/>
						</span>
						<span
							class={cn(
								'min-w-0 flex-1 pt-1 text-sm transition-colors duration-500',
								done || active ? 'font-medium' : 'text-muted-foreground'
							)}
						>
							{step.label}
						</span>
					</li>
				{/each}
			</ol>
		</Card.Content>
	</Card.Root>
</div>

<style>
	.connector {
		background: repeating-linear-gradient(to bottom, var(--fill) 0 2px, transparent 2px 4px);
	}

	.connector.done {
		background: repeating-linear-gradient(
			to bottom,
			color-mix(in oklab, var(--success) 55%, transparent) 0 2px,
			transparent 2px 4px
		);
	}

	.flow-cell {
		opacity: 0.15;
		animation: trickle 1.4s linear infinite;
		animation-delay: calc(var(--cell) * 90ms);
	}

	/* Each cell lights up in turn from the top, so the connector reads as signal flowing on to the next step */
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
		.flow-cell {
			opacity: 0.55;
			animation: none;
		}
	}
</style>
