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
	import { getErrorMessage } from '$lib/utils/error-util';
	import { changedSections } from '$lib/utils/playbook-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { usageFormat } from '$lib/utils/usage-util';
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

	let version = $state.raw<PlaybookVersion | null>(null);
	let versionError = $state<unknown>(null);
	const ops = $derived(run.reflectionOps ?? []);
	const sections = $derived(version ? changedSections(version.previous, version.content) : []);

	// What reflecting cost or how many tokens it used, as the workspace chose
	const usage = $derived(usageFormat());
	const reflectionUsage = $derived(
		usage.pick({ cost: run.reflectionCost, tokens: run.reflectionTokens })
	);

	// The version this run's reflection created carries the content before it, so one request gives the whole diff
	// Both inputs are derived, so refetches of the run that leave them unchanged don't reload the diff
	const jobId = $derived(run.jobId);
	const reflectionVersion = $derived(run.reflectionVersion);
	$effect(() => {
		version = null;
		versionError = null;
		if (reflectionVersion === null) return;
		let stale = false;
		void tryCatch(playbookService.version(jobId, reflectionVersion)).then((result) => {
			if (stale) return;
			version = result.data;
			versionError = result.error;
		});
		return () => {
			stale = true;
		};
	});
</script>

<div class="flex flex-col gap-4">
	{#if run.reflection === 'pending'}
		<Card.Root>
			<Card.Content padding="tight">
				<div class="flex items-center gap-3">
					<Spinner />
					<div class="flex flex-col gap-0.5">
						<p class="text-sm font-medium">Reflecting on this run</p>
						<p class="text-muted-foreground text-sm">
							The job's playbook is updated with what this run taught it. This page updates when
							it's done.
						</p>
					</div>
				</div>
			</Card.Content>
		</Card.Root>
	{:else if run.reflection === 'skipped'}
		<Empty.Root variant="panel" size="md">
			<Empty.Header>
				<Empty.Media variant="icon">
					<GraduationCapIcon />
				</Empty.Media>
				<Empty.Title>Nothing learned from this run yet</Empty.Title>
				<Empty.Description>
					{#if canLearn}
						Turn on learning in the <a href="/jobs/{run.jobId}/settings">job's settings</a> to learn from
						every run, or learn from this one now.
					{:else}
						Runs can be learned from once they succeeded, failed or timed out.
					{/if}
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
						Saved as <a
							href="/jobs/{run.jobId}/playbook"
							class="numeric underline underline-offset-3"
							>playbook version {run.reflectionVersion}</a
						>.
					{:else if run.reflection === 'done'}
						The playbook stayed as it was.
					{/if}
					{#if reflectionUsage > 0}
						Reflection {usage.verb} <span class="numeric">{usage.format(reflectionUsage)}</span>.
					{/if}
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
			<Card.Content>
				<div class="flex flex-col gap-4">
					{#if run.reflectionSummary}
						<p class="text-sm">{run.reflectionSummary}</p>
					{/if}
					{#if ops.length > 0}
						<OpsList {ops} />
					{:else if run.reflection === 'done'}
						<p class="text-muted-foreground text-sm">No changes were needed.</p>
					{/if}
				</div>
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
				<Card.Content>
					<div class="flex flex-col gap-6">
						{#if versionError}
							<p class="text-muted-foreground text-sm">
								{getErrorMessage(versionError, 'Failed to load the playbook changes')}
							</p>
						{:else if !version}
							<Skeleton class="h-40 w-full" />
						{:else}
							{#each sections as section (section.key)}
								<section class="flex flex-col gap-2">
									<h3 class="text-sm font-medium">{section.label}</h3>
									<DiffView before={section.before} after={section.after} />
								</section>
							{:else}
								<p class="text-muted-foreground text-sm">
									No changes compared to the version before.
								</p>
							{/each}
						{/if}
					</div>
				</Card.Content>
			</Card.Root>
		{/if}
	{/if}
</div>
