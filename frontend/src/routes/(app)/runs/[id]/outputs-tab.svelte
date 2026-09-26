<script lang="ts">
	import type { RunArtifact, RunDetail } from '$lib/api/types';
	import Markdown from '$lib/components/markdown.svelte';
	import * as Card from '$lib/components/ui/card';
	import { Skeleton } from '$lib/components/ui/skeleton';
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

	let artifacts = $state<RunArtifact[] | null>(null);
	let artifactsError = $state<unknown>(null);

	// Artifacts are collected when the run ends, so the list reloads once it is final
	$effect(() => {
		void live;
		void loadArtifacts(run.id);
	});

	async function loadArtifacts(id: string) {
		const result = await tryCatch(runService.artifacts(id));
		artifactsError = result.error;
		artifacts = result.data ?? [];
	}

	const hasOutputs = $derived(
		run.outputs !== null &&
			run.outputs !== undefined &&
			!(typeof run.outputs === 'object' && Object.keys(run.outputs as object).length === 0)
	);
	const hasInput = $derived(
		run.input !== null &&
			run.input !== undefined &&
			!(typeof run.input === 'object' && Object.keys(run.input as object).length === 0)
	);
</script>

<div class="grid gap-4 lg:grid-cols-2">
	<Card.Root class="lg:col-span-2">
		<Card.Header>
			<Card.Title>Summary</Card.Title>
			<Card.Description>What the agent reported when it called finish.</Card.Description>
		</Card.Header>
		<Card.Content>
			{#if run.summary}
				<Markdown source={run.summary} />
			{:else}
				<p class="text-muted-foreground text-sm">
					{live ? 'The summary appears when the run finishes.' : 'This run has no summary.'}
				</p>
			{/if}
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title>Outputs</Card.Title>
			<Card.Description>Structured values the run produced.</Card.Description>
		</Card.Header>
		<Card.Content>
			{#if hasOutputs}
				<OutputsView outputs={run.outputs} />
			{:else}
				<p class="text-muted-foreground text-sm">No structured outputs.</p>
			{/if}
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title>Artifacts</Card.Title>
			<Card.Description
				>Files the run wrote to <code class="font-mono">/ump/outputs</code>.</Card.Description
			>
		</Card.Header>
		<Card.Content>
			{#if artifacts === null}
				<Skeleton class="h-8 w-full" />
			{:else if artifactsError}
				<p class="text-muted-foreground text-sm">
					{getErrorMessage(artifactsError, 'Failed to load the artifacts')}
				</p>
			{:else if artifacts.length === 0}
				<p class="text-muted-foreground text-sm">No artifacts.</p>
			{:else}
				<ul class="divide-y rounded-xl border" aria-label="Artifacts">
					{#each artifacts as artifact (artifact.name)}
						<li class="flex items-center gap-3 px-3 py-2 text-sm">
							<FileIcon class="text-muted-foreground size-4 shrink-0" />
							<span class="min-w-0 flex-1 truncate font-mono text-xs">{artifact.name}</span>
							<span class="text-muted-foreground numeric text-xs">{formatBytes(artifact.size)}</span
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

	<Card.Root>
		<Card.Header>
			<Card.Title>Input</Card.Title>
			<Card.Description
				>Available to the run as <code class="font-mono">/ump/input.json</code>.</Card.Description
			>
		</Card.Header>
		<Card.Content>
			{#if hasInput}
				<JsonView value={run.input} class="max-h-80" />
			{:else}
				<p class="text-muted-foreground text-sm">No input.</p>
			{/if}
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title>Run instructions</Card.Title>
			<Card.Description>Extra instructions given for this run only.</Card.Description>
		</Card.Header>
		<Card.Content>
			{#if run.instructions}
				<Markdown source={run.instructions} />
			{:else}
				<p class="text-muted-foreground text-sm">None.</p>
			{/if}
		</Card.Content>
	</Card.Root>
</div>
