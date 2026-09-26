<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Spinner } from '$lib/components/ui/spinner';
	import { formatDuration } from '$lib/utils/format-util';
	import { cn } from '$lib/utils/style';
	import FileCodeIcon from '@lucide/svelte/icons/file-code';
	import FlagIcon from '@lucide/svelte/icons/flag';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import type { Component } from 'svelte';
	import Disclosure from './disclosure.svelte';
	import JsonView from './json-view.svelte';
	import StepShell from './step-shell.svelte';
	import TerminalBlock from './terminal-block.svelte';
	import { commandOf, stripExitCodeLine, type TimelineStep } from './timeline-model';

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

	const output = $derived(step.output);
	const pending = $derived(step.result === null);
	const isError = $derived(output?.isError === true);
	const exitCode = $derived(output?.meta?.exitCode);
	const command = $derived(commandOf(step.args));
	const content = $derived(output?.content ? stripExitCodeLine(output.content) : '');
	const argsText = $derived(step.args === undefined ? '' : JSON.stringify(step.args));

	// `finish` is rendered by its own finish event, so its call only needs a line
	const isFinish = $derived(step.name === 'finish' && !isError);

	const icon = $derived.by<Component>(() => {
		if (step.name === 'finish') return FlagIcon;
		if (command !== null) return TerminalIcon;
		if (['read_file', 'write_file', 'edit_file'].includes(step.name)) return FileCodeIcon;
		return WrenchIcon;
	});

	// A file tool's path is the most useful part of its title
	const subject = $derived.by(() => {
		const args = step.args as Record<string, unknown> | null | undefined;
		if (!args || typeof args !== 'object') return null;
		if (typeof args.path === 'string') return args.path;
		if (typeof args.key === 'string') return args.key;
		return null;
	});
</script>

<StepShell
	{icon}
	tone={isError ? 'danger' : pending && live ? 'live' : 'tool'}
	pulse={pending && live}
	compact={isFinish}
	ts={step.call?.ts ?? step.result?.ts}
	{startTs}
>
	{#snippet title()}
		<span class="font-mono">{step.name}</span>
		{#if subject}
			<span class="text-muted-foreground ml-1 font-mono font-normal">{subject}</span>
		{/if}
	{/snippet}
	{#snippet meta()}
		{#if pending}
			{#if live}
				<span class="inline-flex items-center gap-1 text-blue-600 dark:text-blue-400">
					<Spinner class="size-3" /> running
				</span>
			{:else}
				<span>interrupted</span>
			{/if}
		{:else if step.result?.ms !== null && step.result?.ms !== undefined}
			<span class="numeric">{formatDuration(step.result.ms)}</span>
		{/if}
		{#if exitCode !== undefined && exitCode !== null}
			<span>
				<Badge
					variant={exitCode === 0 ? 'secondary' : 'destructive'}
					class="numeric h-4 rounded-md px-1.5 text-[0.6875rem]"
				>
					exit {exitCode}
				</Badge>
			</span>
		{/if}
		{#if output?.meta?.timedOut}
			<span class="text-red-600 dark:text-red-400">timed out</span>
		{/if}
		{#if output?.meta?.oomKilled}
			<span class="text-red-600 dark:text-red-400">out of memory</span>
		{/if}
		{#if isError && exitCode === undefined}
			<span class="text-red-600 dark:text-red-400">error</span>
		{/if}
	{/snippet}

	{#if !isFinish}
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
			{#if argsText && argsText !== '{}'}
				{#if argsText.length > LONG_ARGS_CHARS}
					<Disclosure label="Arguments" hint="{argsText.length.toLocaleString()} chars">
						<JsonView value={step.args} class="max-h-80" />
					</Disclosure>
				{:else}
					<JsonView value={step.args} class="max-h-80" />
				{/if}
			{/if}
			{#if pending && liveOutput}
				<TerminalBlock content={liveOutput} {live} label="Output of {step.name}" />
			{:else if content}
				<TerminalBlock {content} error={isError} label="Result of {step.name}" />
			{/if}
		{/if}
	{:else if output?.finish?.status}
		<p class={cn('text-muted-foreground -mt-1 text-xs')}>
			Reported <span class="font-medium">{output.finish.status}</span>
		</p>
	{/if}
</StepShell>
