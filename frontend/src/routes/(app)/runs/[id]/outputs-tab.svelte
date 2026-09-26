<script lang="ts">
	import type { RunArtifact, RunDetail } from '$lib/api/types';
	import Markdown from '$lib/components/markdown.svelte';
	import * as Card from '$lib/components/ui/card';
	import RunService from '$lib/services/run-service';
	import { getErrorMessage } from '$lib/utils/error-util';
	import { formatBytes } from '$lib/utils/format-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import FileIcon from '@lucide/svelte/icons/file';
	import JsonView from './json-view.svelte';
	import OutputsView from './outputs-view.svelte';

	let { run, live }: { run: RunDetail; live: boolean } = $props();

	const runService = new RunService();

	let artifacts = $state.raw<RunArtifact[] | null>(null);
	let artifactsError = $state<unknown>(null);

	// Artifacts are collected when the run ends, so the list reloads once it is final
	// The ID is derived, so refetches of the run that change nothing else don't reload the list
	const runId = $derived(run.id);
	$effect(() => {
		void live;
		let stale = false;
		void tryCatch(runService.artifacts(runId)).then((result) => {
			if (stale) return;
			artifactsError = result.error;
			artifacts = result.data ?? [];
		});
		return () => {
			stale = true;
		};
	});

	const hasOutputs = $derived(!isEmpty(run.outputs));
	const hasInput = $derived(!isEmpty(run.input));
	// Most runs write no files, so the card only appears once the list has loaded and has something in it
	const hasArtifacts = $derived(!!artifactsError || (artifacts?.length ?? 0) > 0);
	const noArtifacts = $derived(artifacts !== null && !artifactsError && artifacts.length === 0);

	// Sections without content are named in one line at the end instead of each taking a card
	// Outputs and artifacts only arrive when the run ends, so a live run doesn't call them missing yet
	const missing = $derived(
		[
			!live && !hasOutputs && 'structured outputs',
			!live && noArtifacts && 'artifacts',
			!hasInput && 'input',
			!run.instructions && 'run instructions'
		].filter((part): part is string => !!part)
	);

	function isEmpty(value: unknown) {
		return (
			value === null ||
			value === undefined ||
			(typeof value === 'object' && Object.keys(value).length === 0)
		);
	}

	// 'artifacts, input or run instructions' from a list of names
	function listPhrase(parts: string[]) {
		if (parts.length <= 1) return parts.join('');
		return `${parts.slice(0, -1).join(', ')} or ${parts[parts.length - 1]}`;
	}
</script>

<!-- Cards pair up on wide screens, and one left without a partner spans the row rather than leaving half of it empty -->
<div class="grid items-start gap-4 lg:grid-cols-2 lg:[&>:last-child:nth-child(even)]:col-span-2">
	<Card.Root class="lg:col-span-2">
		<Card.Header>
			<Card.Title>Summary</Card.Title>
			<Card.Description>What the agent reported when it finished.</Card.Description>
		</Card.Header>
		<Card.Content>
			{#if run.summary}
				<Markdown source={run.summary} />
			{:else}
				<p class="text-muted-foreground text-sm">
					{live ? 'The summary appears when the run finishes' : 'This run has no summary'}
				</p>
			{/if}
		</Card.Content>
	</Card.Root>

	{#if hasOutputs}
		<Card.Root>
			<Card.Header>
				<Card.Title>Outputs</Card.Title>
				<Card.Description>Structured values the run produced.</Card.Description>
			</Card.Header>
			<Card.Content>
				<OutputsView outputs={run.outputs} />
			</Card.Content>
		</Card.Root>
	{/if}

	{#if hasArtifacts}
		<Card.Root>
			<Card.Header>
				<Card.Title>Artifacts</Card.Title>
				<Card.Description
					>Files the run wrote to <code class="font-mono">/ump/outputs</code>.</Card.Description
				>
			</Card.Header>
			<Card.Content>
				{#if artifactsError || !artifacts}
					<p class="text-muted-foreground text-sm">
						{getErrorMessage(artifactsError, 'Failed to load the artifacts')}
					</p>
				{:else}
					<ul class="divide-y rounded-lg border" aria-label="Artifacts">
						{#each artifacts as artifact (artifact.name)}
							<li class="flex items-center gap-3 px-3 py-2 text-sm">
								<FileIcon class="text-muted-foreground size-4 shrink-0" />
								<span class="min-w-0 flex-1 truncate font-mono text-xs">{artifact.name}</span>
								<span class="text-muted-foreground numeric text-xs"
									>{formatBytes(artifact.size)}</span
								>
								<a
									href={runService.artifactUrl(run.id, artifact.name)}
									download
									class="text-muted-foreground hover:text-foreground"
									aria-label="Download {artifact.name}"
								>
									<DownloadIcon class="size-4" />
								</a>
							</li>
						{/each}
					</ul>
				{/if}
			</Card.Content>
		</Card.Root>
	{/if}

	{#if hasInput}
		<Card.Root>
			<Card.Header>
				<Card.Title>Input</Card.Title>
				<Card.Description
					>Available to the run as <code class="font-mono">/ump/input.json</code>.</Card.Description
				>
			</Card.Header>
			<Card.Content>
				<JsonView value={run.input} class="max-h-80" />
			</Card.Content>
		</Card.Root>
	{/if}

	{#if run.instructions}
		<Card.Root>
			<Card.Header>
				<Card.Title>Run instructions</Card.Title>
				<Card.Description>Extra instructions given for this run only.</Card.Description>
			</Card.Header>
			<Card.Content>
				<Markdown source={run.instructions} />
			</Card.Content>
		</Card.Root>
	{/if}
</div>

{#if missing.length > 0}
	<p class="text-muted-foreground mt-4 text-sm">
		No {listPhrase(missing)}
	</p>
{/if}
