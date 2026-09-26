<script lang="ts">
	import type { RunEvent } from '$lib/api/types';
	import Markdown from '$lib/components/markdown.svelte';
	import { formatDuration, formatMicroCost, formatTokens } from '$lib/utils/format-util';
	import BotIcon from '@lucide/svelte/icons/bot';
	import Disclosure from './disclosure.svelte';
	import StepShell from './step-shell.svelte';
	import type { LlmCallPayload } from './timeline-model';

	let { event, payload, startTs }: { event: RunEvent; payload: LlmCallPayload; startTs: number } =
		$props();

	// Long answers start collapsed, so a chatty turn doesn't push the tool calls off screen
	const LONG_TEXT_CHARS = 1200;

	const usage = $derived(payload.usage ?? {});
	const text = $derived(payload.text?.trim() ?? '');
	const reasoning = $derived(payload.reasoning?.trim() ?? '');
	const toolCalls = $derived(payload.toolCalls ?? []);
	const unusualStop = $derived(
		payload.stop && !['tool_use', 'end_turn', 'stop'].includes(payload.stop) ? payload.stop : null
	);

	// Persisted events never change, so the initial state only depends on the first render
	let textOpen = $state(text.length <= LONG_TEXT_CHARS);
</script>

<StepShell
	icon={BotIcon}
	tone={payload.error ? 'danger' : 'llm'}
	title={payload.turn ? `Turn ${payload.turn}` : 'Model call'}
	ts={event.ts}
	{startTs}
>
	{#snippet meta()}
		{#if payload.model}
			<span class="font-mono">{payload.model}</span>
		{/if}
		{#if event.ms !== null}
			<span class="numeric">{formatDuration(event.ms)}</span>
		{/if}
		{#if usage.input || usage.output}
			<span class="numeric" title="Input / output tokens">
				{formatTokens(usage.input ?? 0)} in · {formatTokens(usage.output ?? 0)} out
				{#if usage.cacheRead}
					· {formatTokens(usage.cacheRead)} cached
				{/if}
			</span>
		{/if}
		{#if payload.cost}
			<span class="numeric">{formatMicroCost(payload.cost)}</span>
		{/if}
		{#if unusualStop}
			<span class="text-amber-600 dark:text-amber-400">stopped: {unusualStop}</span>
		{/if}
	{/snippet}

	{#if payload.error}
		<p class="text-sm text-red-600 dark:text-red-400">{payload.error}</p>
	{/if}

	{#if reasoning}
		<Disclosure label="Reasoning" hint="{reasoning.length.toLocaleString()} chars">
			<p
				class="text-muted-foreground border-l-2 pl-3 text-sm whitespace-pre-wrap break-words italic"
			>
				{reasoning}
			</p>
		</Disclosure>
	{/if}

	{#if text}
		{#if text.length > LONG_TEXT_CHARS}
			<Disclosure label="Response" hint="{text.length.toLocaleString()} chars" bind:open={textOpen}>
				<Markdown source={text} />
			</Disclosure>
		{:else}
			<Markdown source={text} />
		{/if}
	{/if}

	{#if toolCalls.length > 0}
		<p class="text-muted-foreground flex flex-wrap items-center gap-1 text-xs">
			Calls
			{#each toolCalls as call (call.id)}
				<span class="bg-muted rounded-md px-1.5 py-0.5 font-mono">{call.name}</span>
			{/each}
		</p>
	{/if}
</StepShell>
