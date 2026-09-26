<script lang="ts">
	import type { RunEvent } from '$lib/api/types';
	import Markdown from '$lib/components/markdown.svelte';
	import StatusBadge from '$lib/components/runs/status-badge.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { formatDuration, formatMicroCost, formatTokens } from '$lib/utils/format-util';
	import ActivityIcon from '@lucide/svelte/icons/activity';
	import BoxIcon from '@lucide/svelte/icons/box';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import CircleXIcon from '@lucide/svelte/icons/circle-x';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import FlagIcon from '@lucide/svelte/icons/flag';
	import FoldVerticalIcon from '@lucide/svelte/icons/fold-vertical';
	import PlugIcon from '@lucide/svelte/icons/plug';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import ShieldCheckIcon from '@lucide/svelte/icons/shield-check';
	import SquareTerminalIcon from '@lucide/svelte/icons/square-terminal';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import Disclosure from './disclosure.svelte';
	import JsonView from './json-view.svelte';
	import OutputsView from './outputs-view.svelte';
	import StepShell from './step-shell.svelte';
	import TerminalBlock from './terminal-block.svelte';
	import type { FinishPayload, Usage, VerifyPayload } from './timeline-model';

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

	// Payloads are free-form per event type, so fields are read loosely and checked where they are rendered
	const p = $derived((event.payload ?? {}) as Record<string, any>);
	const ms = $derived(event.ms);
</script>

{#if event.type === 'run.status'}
	<StepShell icon={ActivityIcon} tone="muted" compact title="Status" ts={event.ts} {startTs}>
		{#snippet meta()}
			<span><StatusBadge status={String(p.status ?? '')} /></span>
		{/snippet}
	</StepShell>
{:else if event.type === 'sandbox.create'}
	<StepShell icon={BoxIcon} tone="sandbox" title="Sandbox created" ts={event.ts} {startTs}>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
			{#if p.adapter}<span>{p.adapter}</span>{/if}
			{#if p.isolation}
				<span><Badge variant="outline" class="h-4 rounded-md px-1.5">{p.isolation}</Badge></span>
			{/if}
		{/snippet}
		{#if p.image}
			<p class="text-muted-foreground font-mono text-xs break-all">{p.image}</p>
		{/if}
	</StepShell>
{:else if event.type === 'sandbox.setup'}
	<StepShell
		icon={SquareTerminalIcon}
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
			<TerminalBlock content={String(p.output)} error={!!p.exitCode} label="Setup output" />
		{/if}
	</StepShell>
{:else if event.type === 'sandbox.destroy'}
	<StepShell
		icon={Trash2Icon}
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
	<StepShell
		icon={PlugIcon}
		tone="tool"
		title="MCP · {p.server ?? 'server'}"
		ts={event.ts}
		{startTs}
	>
		{#snippet meta()}
			{#if p.action}<span>{p.action}</span>{/if}
			{#if p.transport}<span>{p.transport}</span>{/if}
			{#if p.tools !== undefined}<span>{p.tools} tools</span>{/if}
			{#if typeof p.ms === 'number'}<span class="numeric">{formatDuration(p.ms)}</span>{/if}
		{/snippet}
	</StepShell>
{:else if event.type === 'broker.call'}
	<!-- Calls the `ump` CLI made from inside the sandbox, recorded by the broker -->
	{@const usage = p.usage as Usage | undefined}
	<StepShell
		icon={SquareTerminalIcon}
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
			{#if usage}
				<span class="numeric">
					{formatTokens(usage.input ?? 0)} in · {formatTokens(usage.output ?? 0)} out
				</span>
			{/if}
			{#if p.cost}<span class="numeric">{formatMicroCost(p.cost)}</span>{/if}
		{/snippet}
		{#if p.error}
			<p class="text-sm text-red-600 dark:text-red-400">{p.error}</p>
		{/if}
		{#if p.result}
			<TerminalBlock content={String(p.result)} error={!!p.isError} label="Result of {p.tool}" />
		{/if}
	</StepShell>
{:else if event.type === 'script.step'}
	<StepShell icon={FlagIcon} tone="default" compact ts={event.ts} {startTs}>
		{#snippet title()}
			Step <span class="font-mono">{p.name}</span>
		{/snippet}
	</StepShell>
{:else if event.type === 'verify.result'}
	{@const verify = event.payload as VerifyPayload}
	<StepShell
		icon={ShieldCheckIcon}
		tone={verify.passed === false ? 'danger' : 'success'}
		title={verify.passed === false ? 'Verification failed' : 'Verification passed'}
		ts={event.ts}
		{startTs}
	>
		<ul class="flex flex-col gap-1 text-sm">
			{#each verify.checks ?? [] as check (check.check)}
				<li class="flex items-start gap-2">
					{#if check.passed}
						<CircleCheckIcon class="mt-0.5 size-4 shrink-0 text-emerald-600" aria-label="Passed" />
					{:else}
						<CircleXIcon class="mt-0.5 size-4 shrink-0 text-red-600" aria-label="Failed" />
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
						<CircleCheckIcon class="mt-0.5 size-4 shrink-0 text-emerald-600" aria-label="Passed" />
					{:else}
						<CircleXIcon class="mt-0.5 size-4 shrink-0 text-red-600" aria-label="Failed" />
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
	<StepShell
		icon={RotateCcwIcon}
		tone="sandbox"
		title="Fell back to the agent"
		ts={event.ts}
		{startTs}
	>
		{#if p.reason || p.message}
			<p class="text-muted-foreground text-sm">{p.reason ?? p.message}</p>
		{/if}
	</StepShell>
{:else if event.type === 'agent.compaction'}
	<StepShell
		icon={FoldVerticalIcon}
		tone={p.error ? 'danger' : 'llm'}
		title="Conversation compacted"
		ts={event.ts}
		{startTs}
	>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
			{#if p.promptTokens}
				<span class="numeric" title="Estimated size of the conversation it replaced">
					{formatTokens(p.promptTokens)} tokens
				</span>
			{/if}
			{#if p.cost}<span class="numeric">{formatMicroCost(p.cost)}</span>{/if}
		{/snippet}
		{#if p.error}
			<p class="text-sm text-red-600 dark:text-red-400">
				The summary failed, so the conversation was kept as it was: {p.error}
			</p>
		{:else}
			<p class="text-muted-foreground text-sm">
				{p.messages} earlier messages were replaced by a summary to stay within the model's context window.
			</p>
			{#if p.summary}
				<Disclosure label="Summary" hint="{String(p.summary).length.toLocaleString()} chars">
					<div class="bg-muted/40 rounded-xl border px-4 py-3">
						<Markdown source={p.summary} />
					</div>
				</Disclosure>
			{/if}
		{/if}
	</StepShell>
{:else if event.type === 'finish'}
	{@const finish = event.payload as FinishPayload}
	{@const success = finish.status === 'success'}
	<StepShell
		icon={success ? CircleCheckIcon : CircleXIcon}
		tone={success ? 'success' : 'danger'}
		title={success ? 'Finished' : 'Finished with a failure'}
		ts={event.ts}
		{startTs}
	>
		{#if finish.summary}
			<div class="bg-muted/40 rounded-xl border px-4 py-3">
				<Markdown source={finish.summary} />
			</div>
		{/if}
		{#if finish.outputs && Object.keys(finish.outputs).length > 0}
			<OutputsView outputs={finish.outputs} />
		{/if}
	</StepShell>
{:else if event.type === 'error'}
	<StepShell icon={CircleAlertIcon} tone="danger" title="Error" ts={event.ts} {startTs}>
		<p class="text-sm break-words whitespace-pre-wrap text-red-700 dark:text-red-400">
			{p.message ?? JSON.stringify(event.payload)}
		</p>
	</StepShell>
{:else if event.type === 'log'}
	<StepShell
		icon={FileTextIcon}
		tone="muted"
		compact
		title={p.message ?? 'Log'}
		ts={event.ts}
		{startTs}
	>
		{#if Array.isArray(p.artifacts) && p.artifacts.length > 0}
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
	<StepShell icon={WrenchIcon} tone="muted" title={event.type} ts={event.ts} {startTs}>
		{#snippet meta()}
			{#if ms !== null}<span class="numeric">{formatDuration(ms)}</span>{/if}
		{/snippet}
		<JsonView value={event.payload} class="max-h-60" />
	</StepShell>
{/if}
