<script lang="ts" module>
	type Group = 'run' | 'sandbox' | 'llm' | 'tool' | 'broker' | 'mcp';

	type Span = {
		key: string;
		group: Group;
		label: string;
		detail?: string;
		start: number;
		end: number;
		error?: boolean;
		// Still in progress, so its end is "now"
		open?: boolean;
		color?: string;
		// Tool names and commands are code, everything else reads as text
		mono?: boolean;
		monoDetail?: boolean;
	};

	const groupLabels: Record<Group, string> = {
		run: 'Run',
		sandbox: 'Sandbox',
		llm: 'Model',
		tool: 'Tools',
		broker: 'ump CLI',
		mcp: 'MCP'
	};

	// The step tokens shared with the timeline's step icons, errors use the status token instead
	const groupColors: Record<Group, string> = {
		run: 'bg-muted-foreground/40',
		sandbox: 'bg-step-sandbox-bar',
		llm: 'bg-step-model-bar',
		tool: 'bg-step-tool-bar',
		broker: 'bg-step-cli-bar',
		mcp: 'bg-step-mcp-bar'
	};

	// The sandbox events as steps, e.g. `sandbox.create` as 'Create sandbox'
	const sandboxLabels: Record<string, string> = {
		'sandbox.create': 'Create sandbox',
		'sandbox.setup': 'Setup script',
		'sandbox.destroy': 'Destroy sandbox',
		'sandbox.image_wait': 'Wait for the job image'
	};

	const GROUP_ORDER: Group[] = ['run', 'sandbox', 'llm', 'tool', 'broker', 'mcp'];

	// Room each tick label gets on the axis, so the longest ones such as '1m 15s' never touch
	const TICK_SPACING_PX = 64;
	// More gridlines than this only add noise, however wide the chart is
	const MAX_INTERVALS = 8;
	// Before the axis has been measured, e.g. on the first frame
	const DEFAULT_INTERVALS = 6;
	// A label closer than this to the right edge ends there instead of sticking out of the chart
	const TICK_EDGE_PX = 24;
</script>

<script lang="ts">
	import type { RunDetail, RunEvent } from '#lib/api/types.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import * as Tooltip from '#lib/components/ui/tooltip/index.js';
	import { secondClock } from '#lib/utils/clock.svelte.js';
	import { cn } from '#lib/utils/style.js';
	import { usageFormat } from '#lib/utils/usage-util.js';
	import ChartGanttIcon from '@lucide/svelte/icons/chart-gantt';
	import {
		commandOf,
		isSettledFinish,
		payloadOf,
		toolDisplay,
		type BrokerCallPayload,
		type CompactionPayload,
		type LlmCallPayload,
		type McpCallPayload,
		type SandboxPayload,
		type ToolCallPayload,
		type ToolResultPayload,
		type Usage
	} from './timeline-model';
	import { formatPreciseDuration, timeAxis } from './waterfall-axis';

	let { run, events, live }: { run: RunDetail; events: RunEvent[]; live: boolean } = $props();

	// A model call's price or its tokens, as the workspace chose, and nothing when it has none of it
	const usage = $derived(usageFormat());
	function usageDetail(payload: { cost?: number; usage?: Usage }) {
		const value = usage.pick({
			cost: payload.cost ?? 0,
			tokens: (payload.usage?.input ?? 0) + (payload.usage?.output ?? 0)
		});
		return value > 0 ? usage.format(value) : null;
	}

	const t0 = $derived(run.queuedAt);
	const now = $derived(live ? secondClock.now : 0);

	// Spans come from events that carry a duration, where the event marks the end of the span
	const spans = $derived.by<Span[]>(() => {
		const out: Span[] = [];

		// The queue and provisioning phases are only recorded on the run itself
		if (run.msQueue) {
			out.push({
				key: 'queue',
				group: 'run',
				label: 'Queue',
				start: run.queuedAt,
				end: run.queuedAt + run.msQueue
			});
		}
		if (run.startedAt && run.msProvision) {
			out.push({
				key: 'provision',
				group: 'run',
				label: 'Provisioning',
				detail: 'Image, sandbox, files and tools',
				start: run.startedAt,
				end: run.startedAt + run.msProvision,
				color: 'bg-step-sandbox-bar/60'
			});
		}

		// Tool calls still waiting for their result, keyed by call ID, which servers without IDs of their own reuse in every turn
		// A plain map is intended here, it only lives while the spans are derived
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const openCalls = new Map<string, { ts: number; payload: ToolCallPayload }>();
		for (const event of events) {
			if (event.type === 'tool.call') {
				const call = payloadOf<ToolCallPayload>(event);
				if (call.callId) openCalls.set(call.callId, { ts: event.ts, payload: call });
				continue;
			}

			if (event.ms === null) continue;
			const span = { key: `e${event.seq}`, start: event.ts - event.ms, end: event.ts };

			switch (event.type) {
				case 'llm.call': {
					const llm = payloadOf<LlmCallPayload>(event);
					out.push({
						...span,
						group: 'llm',
						label: llm.turn ? `Turn ${llm.turn}` : 'Model call',
						// The run's own model goes without saying, only a different one is worth naming
						detail: [llm.model !== run.modelName ? llm.model : null, usageDetail(llm)]
							.filter(Boolean)
							.join(' · '),
						error: !!llm.error
					});
					break;
				}
				case 'agent.compaction': {
					const compaction = payloadOf<CompactionPayload>(event);
					out.push({
						...span,
						group: 'llm',
						label: 'Compaction',
						detail: usageDetail(compaction) ?? undefined,
						error: !!compaction.error
					});
					break;
				}
				case 'tool.result': {
					const result = payloadOf<ToolResultPayload>(event);
					const call = result.callId ? openCalls.get(result.callId) : undefined;
					if (result.callId) openCalls.delete(result.callId);
					const name = result.name ?? 'tool';
					// A finish call takes no time worth drawing, the run's end says the same
					if (isSettledFinish(name, result)) break;
					const command = commandOf(call?.payload.args);
					const display = toolDisplay(name);
					const exitCode = result.meta?.exitCode;
					out.push({
						...span,
						group: 'tool',
						label: display.name,
						detail: [
							command ?? display.source,
							exitCode !== undefined && exitCode !== 0 ? `exit ${exitCode}` : null
						]
							.filter(Boolean)
							.join(' · '),
						error: result.isError,
						mono: true,
						monoDetail: command !== null
					});
					break;
				}
				case 'broker.call': {
					const call = payloadOf<BrokerCallPayload>(event);
					out.push({
						...span,
						group: 'broker',
						label: call.endpoint ?? 'ump',
						detail: call.server ? `${call.server}/${call.tool}` : call.model,
						error: call.ok === false,
						mono: true,
						monoDetail: true
					});
					break;
				}
				case 'mcp.call': {
					const call = payloadOf<McpCallPayload>(event);
					out.push({
						...span,
						group: 'mcp',
						label: call.server ?? 'MCP',
						detail: [
							'Connect',
							call.tools !== undefined
								? `${call.tools} ${call.tools === 1 ? 'tool' : 'tools'}`
								: null
						]
							.filter(Boolean)
							.join(' · ')
					});
					break;
				}
				default:
					if (event.type.startsWith('sandbox.')) {
						const sandbox = payloadOf<SandboxPayload>(event);
						out.push({
							...span,
							group: 'sandbox',
							label: sandboxLabels[event.type] ?? event.type.replace('sandbox.', ''),
							detail: sandbox.image,
							error: !!sandbox.exitCode,
							monoDetail: true
						});
					}
			}
		}

		// Tools still running on a live run grow until their result arrives
		if (live) {
			for (const [callId, call] of openCalls) {
				const name = call.payload.name ?? 'tool';
				if (name === 'finish') continue;
				const command = commandOf(call.payload.args);
				const display = toolDisplay(name);
				out.push({
					key: `open-${callId}`,
					group: 'tool',
					label: display.name,
					detail: command ?? display.source,
					start: call.ts,
					end: Math.max(call.ts, now),
					open: true,
					mono: true,
					monoDetail: command !== null
				});
			}
		}
		return out;
	});

	const end = $derived.by(() => {
		let max = run.finishedAt ?? (live ? now : t0);
		for (const span of spans) max = Math.max(max, span.end);
		return Math.max(max, t0 + 1);
	});
	const total = $derived(end - t0);

	const groups = $derived(
		GROUP_ORDER.map((group) => ({
			group,
			spans: spans.filter((s) => s.group === group).sort((a, b) => a.start - b.start)
		})).filter((g) => g.spans.length > 0)
	);

	// The number of ticks follows the width the chart really has, which the sidebar and the label column take from the viewport
	let axisWidth = $state(0);
	const axis = $derived(
		timeAxis(
			total,
			axisWidth > 0 ? Math.min(MAX_INTERVALS, axisWidth / TICK_SPACING_PX) : DEFAULT_INTERVALS
		)
	);

	function pct(value: number) {
		return `${(value / total) * 100}%`;
	}

	// Labels centre on their tick, except the first, which starts at the chart's left edge, and one near the right edge, which ends there
	function tickAnchor(tick: number) {
		if (tick === 0) return 'translate-x-0';
		if (axisWidth > 0 && axisWidth * (1 - tick / total) < TICK_EDGE_PX) return '-translate-x-full';
		return '-translate-x-1/2';
	}
</script>

{#if spans.length === 0}
	<Empty.Root variant="panel">
		<Empty.Header>
			<Empty.Media variant="icon"><ChartGanttIcon /></Empty.Media>
			<Empty.Title>No timed steps yet</Empty.Title>
			<Empty.Description
				>Steps with a duration show up here as the run progresses.</Empty.Description
			>
		</Empty.Header>
	</Empty.Root>
{:else}
	<!-- A narrow chart stacks each label above its bar, so the bars get the whole width instead of scrolling sideways -->
	<!-- The columns follow the chart's own width rather than the viewport's, which the sidebar narrows at 1024 -->
	<div class="bg-layer ring-hairline @container rounded-lg p-4 ring-1" data-testid="run-waterfall">
		<!-- Time axis -->
		<div class="grid gap-x-3 gap-y-1 @lg:grid-cols-waterfall @4xl:grid-cols-waterfall-wide">
			<div class="text-muted-foreground numeric text-xs">Total {formatPreciseDuration(total)}</div>
			<div class="text-muted-foreground relative h-5 text-xs" bind:clientWidth={axisWidth}>
				{#each axis.ticks as tick (tick)}
					<span
						class={cn('numeric absolute top-0 left-(--tick) whitespace-nowrap', tickAnchor(tick))}
						style:--tick={pct(tick)}>{axis.label(tick)}</span
					>
				{/each}
			</div>
		</div>

		{#each groups as { group, spans: groupSpans } (group)}
			<div class="mt-3">
				<p class="text-muted-foreground mb-1 text-sm font-medium">
					{groupLabels[group]}
				</p>
				{#each groupSpans as span (span.key)}
					<Tooltip.Root>
						<Tooltip.Trigger>
							{#snippet child({ props })}
								<div
									{...props}
									class="hover:bg-muted/60 grid cursor-default items-center gap-x-3 rounded-md @lg:grid-cols-waterfall @4xl:grid-cols-waterfall-wide"
								>
									<div class="flex min-w-0 items-baseline gap-2 px-1 pt-1 text-xs @lg:py-1">
										<span class={cn('max-w-full shrink-0 truncate', span.mono && 'font-mono')}
											>{span.label}</span
										>
										{#if span.detail}
											<span
												class={cn(
													'text-muted-foreground min-w-0 truncate',
													span.monoDetail && 'font-mono'
												)}>{span.detail}</span
											>
										{/if}
									</div>
									<div class="relative h-5">
										<!-- Grid lines at every tick keep bars readable against the axis -->
										{#each axis.ticks as tick (tick)}
											<span
												class="bg-border absolute inset-y-0 left-(--tick) w-px"
												style:--tick={pct(tick)}
											></span>
										{/each}
										<span
											class={cn(
												'absolute top-1 left-(--span-start) h-3 w-(--span-width) min-w-0.5 rounded-sm',
												span.color ?? groupColors[span.group],
												span.error && 'bg-destructive',
												span.open && 'motion-safe:animate-pulse'
											)}
											style:--span-start={pct(span.start - t0)}
											style:--span-width={pct(Math.max(0, span.end - span.start))}
										></span>
									</div>
								</div>
							{/snippet}
						</Tooltip.Trigger>
						<Tooltip.Content side="top" class="max-w-sm">
							<div class="flex flex-col gap-0.5">
								<p class={cn('font-medium', span.mono && 'font-mono')}>{span.label}</p>
								{#if span.detail}
									<p class={cn('break-all opacity-80', span.monoDetail && 'font-mono')}>
										{span.detail}
									</p>
								{/if}
								<p class="numeric opacity-80">
									{formatPreciseDuration(span.end - span.start)}{span.open ? ' so far' : ''} · starts
									at +{formatPreciseDuration(span.start - t0)}
								</p>
							</div>
						</Tooltip.Content>
					</Tooltip.Root>
				{/each}
			</div>
		{/each}
	</div>
{/if}
