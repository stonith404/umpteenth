<script lang="ts">
	import type { PlaybookContent, PlaybookVersion, RunDetail } from '#lib/api/types.js';
	import VersionChange from '#lib/components/playbook/version-change.svelte';
	import VersionSummary from '#lib/components/playbook/version-summary.svelte';
	import RelativeTime from '#lib/components/relative-time.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Card from '#lib/components/ui/card/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import PlaybookService from '#lib/services/playbook-service.js';
	import { getErrorMessage } from '#lib/utils/error-util.js';
	import { opsOutcome, versionChanges } from '#lib/utils/playbook-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import { usageFormat } from '#lib/utils/usage-util.js';
	import ArrowUpRightIcon from '@lucide/svelte/icons/arrow-up-right';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import ClockIcon from '@lucide/svelte/icons/clock';
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

	// Stands in for both sides of the comparison when reflection saved no version
	const EMPTY_PLAYBOOK: PlaybookContent = {
		dockerfile: null,
		learnings: null,
		main: null,
		setup: null,
		toolkit: null
	};

	let version = $state.raw<PlaybookVersion | null>(null);
	let versionError = $state<unknown>(null);
	const ops = $derived(run.reflectionOps ?? []);
	const outcome = $derived(opsOutcome(ops));

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

	// Each proposal becomes a change with the diff it made, while the version is still loading there are none yet
	// Without a saved version every proposal was held back or rejected, so the changes are the proposals alone
	const changes = $derived(
		version
			? versionChanges(version.previous, version.content, ops)
			: reflectionVersion === null
				? versionChanges(EMPTY_PLAYBOOK, EMPTY_PLAYBOOK, ops)
				: []
	);
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

		<!-- Heads the tab like the playbook page heads its current version, so what a run taught reads the same in both places -->
		<section class="flex flex-col gap-4" aria-labelledby="learned-heading">
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div class="flex min-w-0 flex-col gap-1">
					<h2 id="learned-heading" class="text-lg font-semibold">What this run taught the job</h2>
					<div class="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
						{#if run.reflectionVersion}
							<a
								href="/jobs/{run.jobId}/playbook"
								class="text-foreground inline-flex items-center gap-1 link-underline"
							>
								Saved as <span class="numeric">playbook version {run.reflectionVersion}</span>
								<ArrowUpRightIcon class="size-3.5" aria-hidden="true" />
							</a>
						{:else if run.reflection === 'done'}
							<span>The playbook stayed as it was</span>
						{/if}
						{#if version?.createdAt}
							<span class="inline-flex items-center gap-1.5">
								<ClockIcon class="size-3.5" aria-hidden="true" />
								<RelativeTime value={version.createdAt} />
							</span>
						{/if}
						{#if reflectionUsage > 0}
							<span>
								Reflection {usage.verb}
								<span class="numeric">{usage.format(reflectionUsage)}</span>
							</span>
						{/if}
					</div>
				</div>
				{#if canLearn}
					<Button variant="outline" onclick={onLearn}>
						<GraduationCapIcon data-icon="inline-start" />
						Learn again
					</Button>
				{/if}
			</div>

			{#if run.reflectionSummary}
				<VersionSummary author="reflection" summary={run.reflectionSummary} />
			{/if}
		</section>

		<!-- Each proposal sits next to the diff it made, like a version's changes on the playbook page -->
		{#if run.reflection === 'done' || ops.length > 0}
			<section class="flex flex-col gap-3" aria-labelledby="learned-changes-heading">
				<div class="flex flex-wrap items-baseline justify-between gap-x-3">
					<h3 id="learned-changes-heading" class="text-sm font-semibold">Changes</h3>
					{#if outcome}
						<p class="text-muted-foreground text-xs">{outcome}</p>
					{/if}
				</div>
				{#if versionError}
					<p class="text-muted-foreground text-sm">
						{getErrorMessage(versionError, 'Failed to load the playbook changes')}
					</p>
				{:else if reflectionVersion !== null && !version}
					<Skeleton class="h-40 w-full" />
				{:else}
					{#each changes as change, i (change.key)}
						<VersionChange {change} first={i === 0} />
					{:else}
						<p class="text-muted-foreground text-sm">No changes were needed.</p>
					{/each}
				{/if}
			</section>
		{/if}
	{/if}
</div>
