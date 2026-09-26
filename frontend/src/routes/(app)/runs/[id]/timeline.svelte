<script lang="ts">
	import type { RunDetail } from '$lib/api/types';
	import Markdown from '$lib/components/markdown.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import RunService from '$lib/services/run-service';
	import { getErrorMessage } from '$lib/utils/error-util';
	import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
	import ListTreeIcon from '@lucide/svelte/icons/list-tree';
	import EventStep from './event-step.svelte';
	import LlmStep from './llm-step.svelte';
	import type { RunStream } from './run-stream.svelte';
	import StepShell from './step-shell.svelte';
	import { buildTimeline, toolDisplay } from './timeline-model';
	import ToolStep from './tool-step.svelte';

	let {
		run,
		stream,
		live
	}: {
		run: RunDetail;
		stream: RunStream;
		// The run can still change, so the timeline streams and follows new output
		live: boolean;
	} = $props();

	const runService = new RunService();

	const steps = $derived(buildTimeline(stream.events));
	const startTs = $derived(run.queuedAt);
	const turns = $derived(steps.filter((s) => s.kind === 'llm').length);
	const pendingTool = $derived(steps.some((s) => s.kind === 'tool' && s.result === null));
	const hasLiveTurn = $derived(
		live &&
			(stream.liveText !== '' || stream.liveReasoning !== '' || stream.liveToolCalls.length > 0)
	);

	// What the run is doing when nothing streams, so a live timeline never looks frozen
	const waitingLabel = $derived.by(() => {
		if (!live || hasLiveTurn || pendingTool) return null;
		if (run.status === 'queued') return 'Waiting in the queue…';
		if (run.status === 'provisioning') return 'Preparing the sandbox…';
		if (run.status === 'verifying') return 'Verifying the result…';
		if (run.cancelRequested) return 'Stopping…';
		return 'Waiting for the model…';
	});

	// A live run follows its newest output until the user scrolls up, and only the follow button turns it back on
	let follow = $state(true);
	let timeline: HTMLOListElement | undefined = $state();
	let lastScrollTop = 0;
	let touchY = 0;

	// Subpixel scroll positions under page zoom leave the end a fraction of a pixel away
	const END_TOLERANCE_PX = 2;
	// A finger has to travel this far before a touch counts as scrolling rather than a tap
	const TOUCH_SLOP_PX = 8;
	const SCROLL_UP_KEYS = new Set(['ArrowUp', 'PageUp', 'Home']);

	function scroller() {
		return document.scrollingElement ?? document.documentElement;
	}

	function stopFollowing() {
		if (follow && scroller().scrollTop > 0) follow = false;
	}

	function scrollToEnd(behavior: 'instant' | 'smooth' = 'instant') {
		const el = scroller();
		el.scrollTo({ top: el.scrollHeight, behavior });

		// Shrinking content clamps the scroll position upwards, which must not read as the user scrolling away
		lastScrollTop = el.scrollTop;
	}

	// Catches the scrolls no input event announces, such as dragging the scrollbar
	function onScroll() {
		const el = scroller();
		const top = el.scrollTop;
		if (
			follow &&
			top < lastScrollTop &&
			el.scrollHeight - top - el.clientHeight > END_TOLERANCE_PX
		) {
			follow = false;
		}
		lastScrollTop = top;
	}

	// Wheel, touch and keyboard intent stops following right away, since a follow scroll landing mid-gesture would cancel the user's scroll
	function onWheel(event: WheelEvent) {
		// Pinch zoom and sideways swipes over wide output report small upward deltas too
		if (event.ctrlKey || Math.abs(event.deltaX) > Math.abs(event.deltaY)) return;
		if (event.deltaY < 0) stopFollowing();
	}

	function onTouchStart(event: TouchEvent) {
		touchY = event.touches[0].clientY;
	}

	function onTouchMove(event: TouchEvent) {
		// A finger moving down drags the page up
		const y = event.touches[0].clientY;
		if (y - touchY > TOUCH_SLOP_PX) stopFollowing();
		touchY = Math.min(touchY, y);
	}

	// Text fields and menus handle these keys themselves, without scrolling the page
	function handledByTarget(event: KeyboardEvent) {
		const target = event.target;
		if (event.defaultPrevented) return true;
		return (
			target instanceof HTMLElement &&
			(target.isContentEditable || target.matches('input, textarea, select'))
		);
	}

	function onKeydown(event: KeyboardEvent) {
		const scrollsUp = SCROLL_UP_KEYS.has(event.key) || (event.key === ' ' && event.shiftKey);
		if (scrollsUp && !handledByTarget(event)) stopFollowing();
	}

	function followOutput() {
		follow = true;
		scrollToEnd('smooth');
	}

	// Following scrolls once the timeline changes size, which lands after layout and before paint, so streaming tokens neither force extra layouts nor flicker
	$effect(() => {
		if (!timeline || !live) return;
		const observer = new ResizeObserver(() => {
			if (follow) scrollToEnd();
		});
		observer.observe(timeline);
		return () => observer.disconnect();
	});
</script>

<!-- Browsers treat wheel and touch listeners on the window as passive, so none of these delay scrolling -->
<svelte:window
	onscroll={onScroll}
	onwheel={onWheel}
	ontouchstart={onTouchStart}
	ontouchmove={onTouchMove}
	onkeydown={onKeydown}
/>

{#if !stream.loaded}
	<div class="flex flex-col gap-4" aria-busy="true">
		{#each [0, 1, 2, 3] as i (i)}
			<div class="flex gap-3">
				<Skeleton class="size-7 rounded-lg" />
				<div class="flex flex-1 flex-col gap-2 pt-1.5">
					<Skeleton class="h-4 w-48" />
					<Skeleton class="h-16 w-full max-w-2xl" />
				</div>
			</div>
		{/each}
	</div>
{:else if stream.loadError}
	<p class="text-muted-foreground text-sm">
		{getErrorMessage(stream.loadError, 'Failed to load the timeline')}
	</p>
{:else if steps.length === 0 && !live}
	<Empty.Root variant="panel" class="py-12">
		<Empty.Header>
			<Empty.Media variant="icon"><ListTreeIcon /></Empty.Media>
			<Empty.Title>No events</Empty.Title>
			<Empty.Description>
				This run never started, or the retention policy has removed its events.
			</Empty.Description>
		</Empty.Header>
	</Empty.Root>
{:else}
	<ol
		bind:this={timeline}
		class="flex flex-col"
		aria-label="Run timeline"
		data-testid="run-timeline"
	>
		{#each steps as step (step.key)}
			{#if step.kind === 'llm'}
				<LlmStep event={step.event} payload={step.payload} {startTs} runModel={run.modelName} />
			{:else if step.kind === 'tool'}
				<ToolStep {step} {startTs} {live} liveOutput={stream.toolOutput[step.callId]} />
			{:else if step.kind === 'image_wait'}
				<StepShell
					glyph="hourglass"
					tone="sandbox"
					title={step.end ? 'Waited for the job image' : 'Waiting for the job image to build'}
					pulse={!step.end && live}
					ts={step.start.ts}
					{startTs}
				/>
			{:else}
				<EventStep
					event={step.event}
					{startTs}
					artifactUrl={(path) => runService.artifactUrl(run.id, path)}
				/>
			{/if}
		{/each}

		{#if hasLiveTurn}
			<!-- The turn in progress, streamed token by token until its llm.call event replaces it -->
			<StepShell glyph="bot" tone="live" pulse title="Turn {turns + 1}" {startTs}>
				{#snippet meta()}
					<span class="text-info-foreground">streaming</span>
				{/snippet}
				<div class="flex flex-col gap-2" data-testid="live-turn">
					{#if stream.liveReasoning}
						<p
							class="text-muted-foreground border-l-2 pl-3 text-sm whitespace-pre-wrap break-words italic"
						>
							{stream.liveReasoning}
						</p>
					{/if}
					{#if stream.liveText}
						<Markdown source={stream.liveText} />
					{/if}
					{#if stream.liveToolCalls.length > 0}
						<p class="text-muted-foreground flex flex-wrap items-center gap-1 text-xs">
							Calling
							<!-- Named like the tool steps that follow, e.g. front_page rather than toolkit__front_page -->
							{#each stream.liveToolCalls as name, i (i)}
								<span class="bg-muted rounded-md px-1.5 py-0.5 font-mono" title={name}
									>{toolDisplay(name).name}</span
								>
							{/each}
						</p>
					{/if}
				</div>
			</StepShell>
		{/if}

		{#if waitingLabel}
			<StepShell glyph="hourglass" tone="live" pulse compact title={waitingLabel} {startTs} />
		{/if}
	</ol>

	{#if live && !follow}
		<!-- A zero-height sticky anchor, so the button centers on the timeline rather than on the window behind the sidebar -->
		<div class="pointer-events-none sticky bottom-6 z-20 h-0">
			<div class="absolute inset-x-0 bottom-0 flex justify-center">
				<Button class="pointer-events-auto shadow-lg" size="sm" onclick={followOutput}>
					<ArrowDownIcon data-icon="inline-start" />
					Follow output
				</Button>
			</div>
		</div>
	{/if}
{/if}
