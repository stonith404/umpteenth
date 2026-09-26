<script lang="ts">
	import type { PlaybookVersion, RunDetail } from '$lib/api/types';
	import DiffView from '$lib/components/diff-view.svelte';
	import OpsList from '$lib/components/playbook/ops-list.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Empty from '$lib/components/ui/empty';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Spinner } from '$lib/components/ui/spinner';
	import PlaybookService from '$lib/services/playbook-service';
	import { formatMicroCost } from '$lib/utils/format-util';
	import { changedSections } from '$lib/utils/playbook-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import GraduationCapIcon from '@lucide/svelte/icons/graduation-cap';

	let {
		run,
		canLearn,
		onLearn
	}: {
		run: RunDetail;
		// Only finished runs that succeeded, failed or timed out can be learned from
		canLearn: boolean;
		onLearn: () => void;
	} = $props();

	const playbookService = new PlaybookService();

	let version = $state<PlaybookVersion | null>(null);
	let versionLoading = $state(false);
	const ops = $derived(run.reflectionOps ?? []);
	const sections = $derived(version ? changedSections(version.previous, version.content) : []);

	// The version this run's reflection created carries the content before it, so one request gives the whole diff
	$effect(() => {
		const v = run.reflectionVersion;
		version = null;
		if (v === null || v === undefined) return;
		versionLoading = true;
		void tryCatch(playbookService.version(run.jobId, v)).then((result) => {
			if (run.reflectionVersion !== v) return;
			versionLoading = false;
			version = result.data ?? null;
		});
	});
</script>

{#if run.reflection === 'pending'}
	<Card.Root>
		<Card.Content class="flex items-center gap-3 py-2">
			<Spinner />
			<div class="flex flex-col gap-0.5">
				<p class="text-sm font-medium">Reflecting on this run</p>
				<p class="text-muted-foreground text-sm">
					The job's playbook is updated with what this run taught it. This page updates when it's
					done.
				</p>
			</div>
		</Card.Content>
	</Card.Root>
{:else if run.reflection === 'skipped'}
	<Empty.Root class="rounded-2xl border py-10">
		<Empty.Header>
			<Empty.Media variant="icon">
				<GraduationCapIcon />
			</Empty.Media>
			<Empty.Title>Nothing learned from this run yet</Empty.Title>
			<Empty.Description>
				{canLearn
					? 'The job learns automatically when learning is on in its settings. You can also learn from this run once.'
					: 'Runs can be learned from once they succeeded, failed or timed out.'}
			</Empty.Description>
		</Empty.Header>
		{#if canLearn}
			<Empty.Content>
				<Button onclick={onLearn}>
					<GraduationCapIcon data-icon="inline-start" />
					Learn from this run
				</Button>
			</Empty.Content>
		{/if}
	</Empty.Root>
{:else}
	{#if run.reflection === 'failed'}
		<Alert.Root variant="destructive">
			<CircleAlertIcon />
			<Alert.Title>Reflection failed</Alert.Title>
			<Alert.Description class="break-words whitespace-pre-wrap">
				{run.reflectionError ?? 'The reflection did not finish.'}
			</Alert.Description>
		</Alert.Root>
	{/if}

	<Card.Root>
		<Card.Header>
			<Card.Title>What this run taught the job</Card.Title>
			<Card.Description>
				{#if run.reflectionVersion}
					Saved as <a href="/jobs/{run.jobId}/playbook" class="numeric underline underline-offset-3"
						>playbook version {run.reflectionVersion}</a
					>.
				{:else if run.reflection === 'done'}
					The playbook stayed as it was.
				{/if}
				Reflection cost <span class="numeric">{formatMicroCost(run.reflectionCost)}</span>.
			</Card.Description>
			{#if canLearn}
				<Card.Action>
					<Button variant="outline" size="sm" onclick={onLearn}>
						<GraduationCapIcon data-icon="inline-start" />
						Learn again
					</Button>
				</Card.Action>
			{/if}
		</Card.Header>
		<Card.Content class="flex flex-col gap-4">
			{#if run.reflectionSummary}
				<p class="text-sm">{run.reflectionSummary}</p>
			{/if}
			{#if ops.length > 0}
				<OpsList {ops} />
			{:else if run.reflection === 'done'}
				<p class="text-muted-foreground text-sm">No changes were needed.</p>
			{/if}
		</Card.Content>
	</Card.Root>

	{#if run.reflectionVersion}
		<Card.Root>
			<Card.Header>
				<Card.Title>Playbook changes</Card.Title>
				<Card.Description>
					Version {run.reflectionVersion} compared to the version before it.
				</Card.Description>
			</Card.Header>
			<Card.Content class="flex flex-col gap-6">
				{#if versionLoading}
					<Skeleton class="h-40 w-full" />
				{:else}
					{#each sections as section (section.key)}
						<section class="flex flex-col gap-2">
							<h3 class="text-sm font-medium">{section.label}</h3>
							<DiffView before={section.before} after={section.after} />
						</section>
					{:else}
						<p class="text-muted-foreground text-sm">No changes compared to the version before.</p>
					{/each}
				{/if}
			</Card.Content>
		</Card.Root>
	{/if}
{/if}
