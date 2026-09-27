<script lang="ts" module>
	import type { GlyphName } from '$lib/components/pixel-glyph.svelte';
	import type { RunStatus } from '$lib/components/runs/run-meta';
	import type { StatusTone } from '$lib/components/runs/status-badge.svelte';
	import type { StepTone } from './step-shell.svelte';

	// History doesn't pulse, so a live status is a still dot and an outcome has a glyph of its own
	const statusGlyphs: Record<RunStatus, GlyphName> = {
		queued: 'ring',
		provisioning: 'dot',
		running: 'dot',
		verifying: 'dot',
		succeeded: 'check',
		failed: 'cross',
		timed_out: 'timeout',
		cancelled: 'minus',
		skipped: 'minus'
	};

	// The status badge's tones under the names the timeline uses for them
	const statusStepTones: Record<StatusTone, StepTone> = {
		success: 'success',
		danger: 'danger',
		warning: 'warning',
		live: 'live',
		neutral: 'muted'
	};
</script>

<script lang="ts">
	import type { RunEvent } from '$lib/api/types';
	import Markdown from '$lib/components/markdown.svelte';
	import { isLiveStatus, statusLabel } from '$lib/components/runs/run-meta';
	import { statusTone } from '$lib/components/runs/status-badge.svelte';
	import UsageAmount from '$lib/components/usage-amount.svelte';
	import { transportLabel } from '$lib/utils/mcp-util';
	import { formatDuration, formatTokens, sentenceCase } from '$lib/utils/format-util';
	import { usageFormat } from '$lib/utils/usage-util';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import CircleXIcon from '@lucide/svelte/icons/circle-x';
	import Disclosure from './disclosure.svelte';
	import JsonView from './json-view.svelte';
	import OutputsView from './outputs-view.svelte';
	import StepShell from './step-shell.svelte';
	import TerminalBlock from './terminal-block.svelte';
	import {
		payloadOf,
		type BrokerCallPayload,
		type CompactionPayload,
		type FinishPayload,
		type McpCallPayload,
		type NotePayload,
		type SandboxPayload,
		type Usage,
		type VerifyPayload
	} from './timeline-model';

	let {
		event,
		startTs,
		artifactUrl
	}: {
		event: RunEvent;
		startTs: number;
		// Download link of an artifact named in a `log` event
		artifactUrl: (path: string) => string;
	} = $props();

	const ms = $derived(event.ms);

	// Model calls show their price or their tokens, as the workspace chose, and nothing when they have none of it
	const usageShown = $derived(usageFormat());
	function usageOf(payload: { cost?: number; usage?: Usage }) {
		return {
			cost: payload.cost ?? 0,
			tokens: (payload.usage?.input ?? 0) + (payload.usage?.output ?? 0)
		};
	}
</script>

{#if event.type === 'run.status'}
	<!-- A status change is a milestone rather than a step, so it is one line named after the status, muted until the run reaches an outcome -->
	{@const status = payloadOf<NotePayload>(event).status ?? ''}
	<StepShell
		glyph={statusGlyphs[status as RunStatus] ?? 'ring'}
		tone={statusStepTones[statusTone(status)]}
		compact
		quiet={isLiveStatus(status)}
		title={statusLabel(status)}
		ts={event.ts}
		{startTs}
	/>
{:else if event.type === 'sandbox.create'}
	{@const p = payloadOf<SandboxPayload>(event)}
	<StepShell glyph="crate" tone="sandbox" title="Sandbox created" ts={event.ts} {startTs}>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
		{/snippet}
		{#if p.image}
			<p class="text-muted-foreground font-mono text-xs break-all">{p.image}</p>
		{/if}
	</StepShell>
{:else if event.type === 'sandbox.setup'}
	{@const p = payloadOf<SandboxPayload>(event)}
	<StepShell
		glyph="prompt"
		tone={p.exitCode ? 'danger' : 'sandbox'}
		title="Setup script"
		ts={event.ts}
		{startTs}
	>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
			{#if p.exitCode !== undefined}<span class="numeric">exit {p.exitCode}</span>{/if}
		{/snippet}
		{#if p.output}
			<TerminalBlock content={p.output} error={!!p.exitCode} label="Setup output" />
		{/if}
	</StepShell>
{:else if event.type === 'sandbox.destroy'}
	<StepShell
		glyph="dissolve"
		tone="muted"
		compact
		title="Sandbox destroyed"
		ts={event.ts}
		{startTs}
	>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
		{/snippet}
	</StepShell>
{:else if event.type === 'mcp.call'}
	{@const p = payloadOf<McpCallPayload>(event)}
	<!-- The runner records one of these for each MCP server it connects to -->
	<StepShell
		glyph="plug"
		tone="tool"
		title="Connected to {p.server ?? 'an MCP server'}"
		ts={event.ts}
		{startTs}
	>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
			{#if p.transport}<span>{transportLabel(p.transport)}</span>{/if}
			{#if p.tools !== undefined}<span>{p.tools} {p.tools === 1 ? 'tool' : 'tools'}</span>{/if}
		{/snippet}
	</StepShell>
{:else if event.type === 'broker.call'}
	<!-- Calls the `ump` CLI made from inside the sandbox, recorded by the broker -->
	{@const p = payloadOf<BrokerCallPayload>(event)}
	{@const usage = usageOf(p)}
	<StepShell
		glyph="ump"
		tone={p.ok === false || p.isError ? 'danger' : 'tool'}
		ts={event.ts}
		{startTs}
	>
		{#snippet title()}
			<span class="font-mono">ump</span>
			<span class="text-muted-foreground ml-1 font-mono font-normal">{p.endpoint}</span>
		{/snippet}
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
			{#if p.server}<span class="font-mono">{p.server}/{p.tool}</span>{/if}
			{#if p.model}<span class="font-mono">{p.model}</span>{/if}
			{#if usageShown.pick(usage) > 0}
				<UsageAmount
					{usage}
					breakdown={{
						input: p.usage?.input ?? 0,
						output: p.usage?.output ?? 0,
						cacheRead: p.usage?.cacheRead,
						cacheWrite: p.usage?.cacheWrite
					}}
				/>
			{/if}
		{/snippet}
		{#if p.error}
			<p class="text-destructive text-sm">{sentenceCase(p.error)}</p>
		{/if}
		{#if p.result}
			<TerminalBlock content={p.result} error={!!p.isError} label="Result of {p.tool}" />
		{/if}
	</StepShell>
{:else if event.type === 'script.step'}
	{@const p = payloadOf<NotePayload>(event)}
	<StepShell glyph="play" tone="default" compact ts={event.ts} {startTs}>
		{#snippet title()}
			Step <span class="font-mono">{p.name}</span>
		{/snippet}
	</StepShell>
{:else if event.type === 'verify.result'}
	{@const verify = payloadOf<VerifyPayload>(event)}
	<StepShell
		glyph="shield"
		tone={verify.passed === false ? 'danger' : 'success'}
		title={verify.passed === false ? 'Verification failed' : 'Verification passed'}
		ts={event.ts}
		{startTs}
	>
		<ul class="flex flex-col gap-1 text-sm">
			{#each verify.checks ?? [] as check (check.check)}
				<li class="flex items-start gap-2">
					{#if check.passed}
						<CircleCheckIcon class="text-success mt-0.5 size-4 shrink-0" aria-label="Passed" />
					{:else}
						<CircleXIcon class="text-destructive mt-0.5 size-4 shrink-0" aria-label="Failed" />
					{/if}
					<span class="min-w-0">
						<code class="font-mono text-xs">{check.check}</code>
						{#if check.detail}<span class="text-muted-foreground"> · {check.detail}</span>{/if}
					</span>
				</li>
			{/each}
			{#if verify.llm}
				<li class="flex items-start gap-2">
					{#if verify.llm.pass}
						<CircleCheckIcon class="text-success mt-0.5 size-4 shrink-0" aria-label="Passed" />
					{:else}
						<CircleXIcon class="text-destructive mt-0.5 size-4 shrink-0" aria-label="Failed" />
					{/if}
					<span class="min-w-0">
						Judged by the utility model<span class="text-muted-foreground">
							· {verify.llm.reason}</span
						>
					</span>
				</li>
			{/if}
		</ul>
	</StepShell>
{:else if event.type === 'fallback'}
	{@const p = payloadOf<NotePayload>(event)}
	<StepShell glyph="rewind" tone="sandbox" title="Fell back to the agent" ts={event.ts} {startTs}>
		{#if p.reason}
			<p class="text-muted-foreground text-sm">{p.reason}</p>
		{/if}
	</StepShell>
{:else if event.type === 'agent.compaction'}
	{@const p = payloadOf<CompactionPayload>(event)}
	{@const usage = usageOf(p)}
	<StepShell
		glyph="fold"
		tone={p.error ? 'danger' : 'llm'}
		title="Conversation compacted"
		ts={event.ts}
		{startTs}
	>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
			{#if usageShown.pick(usage) > 0}
				<UsageAmount {usage} />
			{/if}
		{/snippet}
		{#if p.error}
			<p class="text-destructive text-sm">
				The summary failed, so the conversation was kept as it was: {p.error}
			</p>
		{:else}
			<p class="text-muted-foreground text-sm">
				<!-- The size of the conversation explains the compaction in the model's context window, which is counted in tokens whatever unit usage shows in -->
				{p.messages} earlier messages{#if p.promptTokens}, about {formatTokens(p.promptTokens)}
					tokens,{/if} were replaced by a summary to stay within the model's context window.
			</p>
			{#if p.summary}
				<Disclosure label="Summary" hint="{p.summary.length.toLocaleString('en-US')} chars">
					<div class="bg-muted/40 rounded-lg border px-4 py-3">
						<Markdown source={p.summary} />
					</div>
				</Disclosure>
			{/if}
		{/if}
	</StepShell>
{:else if event.type === 'finish'}
	{@const finish = payloadOf<FinishPayload>(event)}
	{@const success = finish.status === 'success'}
	<StepShell
		glyph="flag"
		tone={success ? 'success' : 'danger'}
		title={success ? 'Finished' : 'Finished with a failure'}
		ts={event.ts}
		{startTs}
	>
		{#if finish.summary}
			<div class="bg-muted/40 rounded-lg border px-4 py-3">
				<Markdown source={finish.summary} />
			</div>
		{/if}
		{#if finish.outputs && Object.keys(finish.outputs).length > 0}
			<OutputsView outputs={finish.outputs} />
		{/if}
	</StepShell>
{:else if event.type === 'error'}
	{@const p = payloadOf<NotePayload>(event)}
	<StepShell glyph="alert" tone="danger" title="Error" ts={event.ts} {startTs}>
		<p class="text-destructive text-sm break-words whitespace-pre-wrap">
			{p.message ? sentenceCase(p.message) : JSON.stringify(event.payload)}
		</p>
	</StepShell>
{:else if event.type === 'log'}
	{@const p = payloadOf<NotePayload>(event)}
	<StepShell glyph="file" tone="muted" compact title={p.message ?? 'Log'} ts={event.ts} {startTs}>
		{#if p.artifacts && p.artifacts.length > 0}
			<ul class="flex flex-wrap gap-1.5">
				{#each p.artifacts as path (path)}
					<li>
						<a
							href={artifactUrl(path)}
							download
							class="bg-muted hover:bg-muted/70 rounded-md px-1.5 py-0.5 font-mono text-xs"
						>
							{path}
						</a>
					</li>
				{/each}
			</ul>
		{/if}
	</StepShell>
{:else}
	<StepShell glyph="wrench" tone="muted" title={event.type} ts={event.ts} {startTs}>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
		{/snippet}
		<JsonView value={event.payload} class="max-h-60" />
	</StepShell>
{/if}
