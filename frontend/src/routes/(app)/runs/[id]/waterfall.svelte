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
	};

	const groupLabels: Record<Group, string> = {
		run: 'Run',
		sandbox: 'Sandbox',
		llm: 'LLM',
		tool: 'Tools',
		broker: 'ump CLI',
		mcp: 'MCP'
	};

	const groupColors: Record<Group, string> = {
		run: 'bg-zinc-400 dark:bg-zinc-500',
		sandbox: 'bg-amber-500',
		llm: 'bg-fuchsia-500',
		tool: 'bg-sky-500',
		broker: 'bg-teal-500',
		mcp: 'bg-indigo-500'
	};

	const GROUP_ORDER: Group[] = ['run', 'sandbox', 'llm', 'tool', 'broker', 'mcp'];

	// Tick spacings the axis picks from, so labels land on round durations
	const TICK_STEPS = [
		10, 25, 50, 100, 250, 500, 1_000, 2_000, 5_000, 10_000, 15_000, 30_000, 60_000, 120_000,
		300_000, 600_000, 900_000, 1_800_000, 3_600_000
	];
	const MAX_TICKS = 8;
</script>

<script lang="ts">
	import type { RunDetail, RunEvent } from '$lib/api/types';
	import * as Empty from '$lib/components/ui/empty';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { secondClock } from '$lib/components/runs/run-duration.svelte';
	import { formatDuration, formatMicroCost, formatTokens } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import ChartGanttIcon from '@lucide/svelte/icons/chart-gantt';
	import { commandOf, type LlmCallPayload, type ToolResultPayload } from './timeline-model';

	let { run, events, live }: { run: RunDetail; events: RunEvent[]; live: boolean } = $props();

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
				end: run.queuedAt + run.msQueue,
				color: 'bg-zinc-400 dark:bg-zinc-500'
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
				color: 'bg-amber-500/60'
			});
		}

		// Plain collections are intended here, they only live while the spans are derived
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const toolCalls = new Map<string, RunEvent>();
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const finishedCalls = new Set<string>();
		for (const event of events) {
			const p = (event.payload ?? {}) as Record<string, any>;
			const key = `e${event.seq}`;

			if (event.type === 'tool.call' && typeof p.callId === 'string') {
				toolCalls.set(p.callId, event);
				continue;
			}

			// MCP connects carry their duration in the payload
			const ms = event.ms ?? (event.type === 'mcp.call' && typeof p.ms === 'number' ? p.ms : null);
			if (ms === null) continue;
			const span = { key, start: event.ts - ms, end: event.ts };

			switch (event.type) {
				case 'llm.call': {
					const llm = p as LlmCallPayload;
					const usage = llm.usage;
					out.push({
						...span,
						group: 'llm',
						label: llm.turn ? `Turn ${llm.turn}` : 'Model call',
						detail: [
							llm.model,
							usage
								? `${formatTokens(usage.input ?? 0)} in · ${formatTokens(usage.output ?? 0)} out`
								: null,
							llm.cost ? formatMicroCost(llm.cost) : null
						]
							.filter(Boolean)
							.join(' · '),
						error: !!llm.error
					});
					break;
				}
				case 'agent.compaction':
					out.push({
						...span,
						group: 'llm',
						label: 'Compaction',
						detail: [
							p.usage ? `${formatTokens(p.usage.input ?? 0)} in` : null,
							p.cost ? formatMicroCost(p.cost) : null
						]
							.filter(Boolean)
							.join(' · '),
						error: !!p.error
					});
					break;
				case 'tool.result': {
					const result = p as ToolResultPayload;
					if (result.callId) finishedCalls.add(result.callId);
					const call = result.callId ? toolCalls.get(result.callId) : undefined;
					const command = commandOf((call?.payload as { args?: unknown } | null)?.args);
					out.push({
						...span,
						group: 'tool',
						label: result.name ?? 'tool',
						detail: [
							command,
							result.meta?.exitCode !== undefined ? `exit ${result.meta.exitCode}` : null
						]
							.filter(Boolean)
							.join(' · '),
						error: result.isError
					});
					break;
				}
				case 'broker.call':
					out.push({
						...span,
						group: 'broker',
						label: String(p.endpoint ?? 'ump'),
						detail: p.server ? `${p.server}/${p.tool}` : p.model ? String(p.model) : undefined,
						error: p.ok === false
					});
					break;
				case 'mcp.call':
					out.push({
						...span,
						group: 'mcp',
						label: String(p.server ?? 'MCP'),
						detail: [p.action, p.tools !== undefined ? `${p.tools} tools` : null]
							.filter(Boolean)
							.join(' · ')
					});
					break;
				default:
					if (event.type.startsWith('sandbox.')) {
						out.push({
							...span,
							group: 'sandbox',
							label: event.type.replace('sandbox.', '').replace('_', ' '),
							detail: typeof p.image === 'string' ? p.image : undefined,
							error: typeof p.exitCode === 'number' && p.exitCode !== 0
						});
					}
			}
		}

		// Tools still running on a live run grow until their result arrives
		if (live) {
			for (const [callId, call] of toolCalls) {
				if (finishedCalls.has(callId)) continue;
				const p = (call.payload ?? {}) as Record<string, any>;
				out.push({
					key: `open-${callId}`,
					group: 'tool',
					label: String(p.name ?? 'tool'),
					detail: commandOf(p.args) ?? undefined,
					start: call.ts,
					end: Math.max(call.ts, now),
					open: true
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

	const ticks = $derived.by(() => {
		const step =
			TICK_STEPS.find((s) => total / s <= MAX_TICKS) ?? TICK_STEPS[TICK_STEPS.length - 1];
		const out: number[] = [];
		for (let t = 0; t <= total; t += step) out.push(t);
		return out;
	});

	function pct(value: number) {
		return `${(value / total) * 100}%`;
	}
</script>

{#if spans.length === 0}
	<Empty.Root class="border border-dashed py-12">
		<Empty.Header>
			<Empty.Media variant="icon"><ChartGanttIcon /></Empty.Media>
			<Empty.Title>No timed steps yet</Empty.Title>
			<Empty.Description
				>Steps with a duration show up here as the run progresses.</Empty.Description
			>
		</Empty.Header>
	</Empty.Root>
{:else}
	<div class="bg-card overflow-x-auto rounded-2xl border" data-testid="run-waterfall">
		<div class="min-w-[36rem] p-4">
			<!-- Time axis -->
			<div class="grid grid-cols-[9rem_1fr] gap-3 sm:grid-cols-[14rem_1fr]">
				<div class="text-muted-foreground text-xs">Total {formatDuration(total)}</div>
				<div class="text-muted-foreground relative h-5 text-xs">
					{#each ticks as tick (tick)}
						<span
							class="numeric absolute top-0 -translate-x-1/2 first:translate-x-0"
							style:left={pct(tick)}>{formatDuration(tick)}</span
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
										class="hover:bg-muted/60 grid cursor-default grid-cols-[9rem_1fr] items-center gap-3 rounded-md sm:grid-cols-[14rem_1fr]"
									>
										<div class="flex min-w-0 items-baseline gap-2 py-1 pl-1 text-xs">
											<span class="max-w-full shrink-0 truncate font-mono">{span.label}</span>
											{#if span.detail}
												<span class="text-muted-foreground hidden min-w-0 truncate sm:inline"
													>{span.detail}</span
												>
											{/if}
										</div>
										<div class="relative h-5">
											<!-- Grid lines at every tick keep bars readable against the axis -->
											{#each ticks as tick (tick)}
												<span class="bg-border absolute inset-y-0 w-px" style:left={pct(tick)}
												></span>
											{/each}
											<span
												class={cn(
													'absolute top-1 h-3 min-w-[2px] rounded-sm',
													span.color ?? groupColors[span.group],
													span.error && 'bg-red-500',
													span.open && 'animate-pulse'
												)}
												style:left={pct(span.start - t0)}
												style:width={pct(Math.max(0, span.end - span.start))}
											></span>
										</div>
									</div>
								{/snippet}
							</Tooltip.Trigger>
							<Tooltip.Content side="top" class="max-w-sm">
								<div class="flex flex-col gap-0.5">
									<p class="font-medium">{span.label}</p>
									{#if span.detail}
										<p class="font-mono break-all opacity-80">{span.detail}</p>
									{/if}
									<p class="numeric opacity-80">
										{formatDuration(span.end - span.start)}{span.open ? ' so far' : ''} · starts at +{formatDuration(
											span.start - t0
										)}
									</p>
								</div>
							</Tooltip.Content>
						</Tooltip.Root>
					{/each}
				</div>
			{/each}
		</div>
	</div>
{/if}
