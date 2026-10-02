<script lang="ts">
	import type { RunEvent } from '#lib/api/types.js';
	import Markdown from '#lib/components/markdown.svelte';
	import UsageAmount from '#lib/components/usage-amount.svelte';
	import { formatDuration, sentenceCase } from '#lib/utils/format-util.js';
	import { usageFormat } from '#lib/utils/usage-util.js';
	import Disclosure from './disclosure.svelte';
	import StepShell from './step-shell.svelte';
	import type { LlmCallPayload } from './timeline-model';

	let {
		event,
		payload,
		startTs,
		runModel
	}: {
		event: RunEvent;
		payload: LlmCallPayload;
		startTs: number;
		// The run's model ID, so a turn only names its model when it differs, e.g. after a fallback
		runModel?: string | null;
	} = $props();

	// Long answers start collapsed, so a chatty turn doesn't push the tool calls off screen
	const LONG_TEXT_CHARS = 1200;

	const tokenCounts = $derived(payload.usage ?? {});
	// The turn's price or its tokens, as the workspace chose, and nothing when a call has none of it, like a free local model's price
	const usage = $derived({
		cost: payload.cost ?? 0,
		tokens: (tokenCounts.input ?? 0) + (tokenCounts.output ?? 0)
	});
	const hasUsage = $derived(usageFormat().pick(usage) > 0);
	const text = $derived(payload.text?.trim() ?? '');
	const reasoning = $derived(payload.reasoning?.trim() ?? '');
	const otherModel = $derived(payload.model && payload.model !== runModel ? payload.model : null);
	const unusualStop = $derived(
		payload.stop && !['tool_use', 'end_turn'].includes(payload.stop) ? payload.stop : null
	);

	// Persisted events never change, so the initial state only depends on the first render
	let textOpen = $state(text.length <= LONG_TEXT_CHARS);
</script>

<!-- The tool calls of a turn follow as steps of their own, so the turn doesn't list them again -->
<StepShell
	glyph="bot"
	tone={payload.error ? 'danger' : 'llm'}
	title={payload.turn ? `Turn ${payload.turn}` : 'Model call'}
	ts={event.ts}
	{startTs}
>
	{#snippet meta()}
		{#if otherModel}
			<span class="font-mono">{otherModel}</span>
		{/if}
		{#if event.ms !== null}
			<span class="numeric">{formatDuration(event.ms)}</span>
		{/if}
		{#if hasUsage}
			<UsageAmount
				{usage}
				breakdown={{
					input: tokenCounts.input ?? 0,
					output: tokenCounts.output ?? 0,
					cacheRead: tokenCounts.cacheRead,
					cacheWrite: tokenCounts.cacheWrite
				}}
			/>
		{/if}
		{#if unusualStop}
			<span class="text-warning-foreground">stopped: {unusualStop}</span>
		{/if}
	{/snippet}

	{#if payload.error}
		<p class="text-destructive text-sm">{sentenceCase(payload.error)}</p>
	{/if}

	{#if reasoning}
		<Disclosure label="Reasoning" hint="{reasoning.length.toLocaleString('en-US')} chars">
			<p
				class="text-muted-foreground border-l-2 pl-3 text-sm whitespace-pre-wrap break-words italic"
			>
				{reasoning}
			</p>
		</Disclosure>
	{/if}

	{#if text}
		{#if text.length > LONG_TEXT_CHARS}
			<Disclosure
				label="Response"
				hint="{text.length.toLocaleString('en-US')} chars"
				bind:open={textOpen}
			>
				<Markdown source={text} />
			</Disclosure>
		{:else}
			<Markdown source={text} />
		{/if}
	{/if}
</StepShell>
