<script lang="ts">
	import type { GlyphName } from '#lib/components/pixel-glyph.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import { formatDuration } from '#lib/utils/format-util.js';
	import Disclosure from './disclosure.svelte';
	import JsonView from './json-view.svelte';
	import StepShell from './step-shell.svelte';
	import TerminalBlock from './terminal-block.svelte';
	import {
		commandOf,
		parseJsonResult,
		stripExitCodeLine,
		toolDisplay,
		type TimelineStep
	} from './timeline-model';

	let {
		step,
		startTs,
		liveOutput,
		live = false
	}: {
		step: Extract<TimelineStep, { kind: 'tool' }>;
		startTs: number;
		// Output streamed while the tool runs, replaced by the persisted result
		liveOutput?: string;
		// The run is still executing, so a call without a result is in progress rather than abandoned
		live?: boolean;
	} = $props();

	// Arguments larger than this start collapsed
	const LONG_ARGS_CHARS = 600;

	// The timeline rebuilds its steps on every event, and reading the payloads through deriveds skips the work when they are unchanged
	const args = $derived(step.args);
	const output = $derived(step.output);
	const pending = $derived(step.result === null);
	const isError = $derived(output?.isError === true);
	const exitCode = $derived(output?.meta?.exitCode);
	const command = $derived(commandOf(args));
	const content = $derived(output?.content ? stripExitCodeLine(output.content) : '');
	const json = $derived(command === null && content ? parseJsonResult(content) : undefined);
	const argsText = $derived(args === undefined ? '' : JSON.stringify(args));
	const display = $derived(toolDisplay(step.name));
	const isScript = $derived(step.name.startsWith('toolkit__'));

	const glyph = $derived.by<GlyphName>(() => {
		if (step.name === 'finish') return 'flag';
		if (command !== null) return 'prompt';
		if (isScript) return 'list';
		if (display.source) return 'plug';
		if (['read_file', 'write_file', 'edit_file'].includes(step.name)) return 'file-code';
		return 'wrench';
	});

	// A file tool's path is the most useful part of its title
	const subject = $derived.by(() => {
		if (!args || typeof args !== 'object') return null;
		if ('path' in args && typeof args.path === 'string') return args.path;
		if ('key' in args && typeof args.key === 'string') return args.key;
		return null;
	});
</script>

<StepShell
	{glyph}
	tone={isError ? 'danger' : pending && live ? 'live' : 'tool'}
	pulse={pending && live}
	ts={step.call?.ts ?? step.result?.ts}
	{startTs}
>
	{#snippet title()}
		<!-- Namespaced names like toolkit__front_page read as the tool's own name, the raw name stays in the accessible names below -->
		<span class="font-mono">{display.name}</span>
		{#if subject}
			<span class="text-muted-foreground ml-1 font-mono font-normal">{subject}</span>
		{/if}
	{/snippet}
	{#snippet meta()}
		{#if display.source}
			<span>{display.source}</span>
		{/if}
		{#if pending}
			{#if live}
				<span class="text-info-foreground inline-flex items-center gap-1">
					<Spinner class="size-3" /> running
				</span>
			{:else}
				<span>interrupted</span>
			{/if}
		{:else if step.result?.ms !== null && step.result?.ms !== undefined}
			<span class="numeric">{formatDuration(step.result.ms)}</span>
		{/if}
		<!-- A clean exit is the normal case, only a failing one earns a badge -->
		{#if exitCode !== undefined && exitCode !== null && exitCode !== 0}
			<span class="numeric"><Badge variant="destructive">exit {exitCode}</Badge></span>
		{/if}
		{#if output?.meta?.timedOut}
			<span class="text-destructive">timed out</span>
		{/if}
		{#if output?.meta?.oomKilled}
			<span class="text-destructive">out of memory</span>
		{/if}
		{#if isError && exitCode === undefined}
			<span class="text-destructive">error</span>
		{/if}
	{/snippet}

	{#if command !== null}
		<!-- Shell tools read like a terminal session: the command, then what it printed -->
		<TerminalBlock
			{command}
			content={pending ? liveOutput : content}
			live={pending && live}
			error={isError}
			label="Output of {step.name}"
		/>
	{:else}
		{@const hasArgs = !!argsText && argsText !== '{}'}
		{@const hasResult = (pending && !!liveOutput) || !!content}
		<!-- Captions only help once there are two boxes to tell apart -->
		{@const captions = hasArgs && hasResult}
		{#if hasArgs}
			{#if argsText.length > LONG_ARGS_CHARS}
				<Disclosure label="Arguments" hint="{argsText.length.toLocaleString('en-US')} chars">
					<JsonView value={args} class="max-h-80" />
				</Disclosure>
			{:else}
				<div class="flex flex-col gap-1.5">
					{#if captions}<p class="text-muted-foreground text-xs font-medium">Arguments</p>{/if}
					<JsonView value={args} class="max-h-80" />
				</div>
			{/if}
		{/if}
		{#if pending && liveOutput}
			<TerminalBlock content={liveOutput} {live} label="Output of {step.name}" />
		{:else if content}
			<div class="flex flex-col gap-1.5">
				{#if captions}<p class="text-muted-foreground text-xs font-medium">Result</p>{/if}
				{#if json !== undefined && !isError}
					<!-- A JSON result is shown pretty-printed like the arguments, rather than as one wrapped terminal line -->
					<div role="region" aria-label="Result of {step.name}">
						<JsonView value={json} class="max-h-80" />
					</div>
				{:else}
					<TerminalBlock {content} error={isError} label="Result of {step.name}" />
				{/if}
			</div>
		{/if}
	{/if}
</StepShell>
