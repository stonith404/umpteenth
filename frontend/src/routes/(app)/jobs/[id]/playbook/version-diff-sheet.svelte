<script lang="ts">
	import type { PlaybookAppliedOp, PlaybookVersion } from '$lib/api/types';
	import VersionChange from '$lib/components/playbook/version-change.svelte';
	import VersionMeta from '$lib/components/playbook/version-meta.svelte';
	import VersionSummary from '$lib/components/playbook/version-summary.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Sheet from '$lib/components/ui/sheet';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import PlaybookService from '$lib/services/playbook-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { opStatusLabel, versionChanges } from '$lib/utils/playbook-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';

	let {
		jobId,
		version = $bindable(null),
		currentVersion,
		onRollback
	}: {
		jobId: string;
		// The version to show, the sheet is open while it is set
		version: number | null;
		currentVersion: number;
		onRollback: (version: number) => void;
	} = $props();

	const playbookService = new PlaybookService();

	let detail = $state<PlaybookVersion | null>(null);
	let loading = $state(false);

	const changes = $derived(
		detail ? versionChanges(detail.previous, detail.content, detail.ops ?? []) : []
	);

	const statuses: PlaybookAppliedOp['status'][] = ['applied', 'held', 'rejected'];

	// How the proposals ended, e.g. `2 applied · 1 rejected`
	const outcome = $derived.by(() => {
		const ops = detail?.ops ?? [];
		return statuses
			.map((status) => [status, ops.filter((op) => op.status === status).length] as const)
			.filter(([, n]) => n > 0)
			.map(([status, n]) => `${n} ${opStatusLabel(status).toLowerCase()}`)
			.join(' · ');
	});

	// The version endpoint includes the previous version's content, so one request gives the whole diff
	$effect(() => {
		const v = version;
		if (v === null) return;
		detail = null;
		loading = true;

		// Closing the sheet or picking another version drops this response, even when the same version is reopened meanwhile
		let cancelled = false;
		void tryCatch(playbookService.version(jobId, v)).then((result) => {
			if (cancelled) return;
			loading = false;
			if (result.error) {
				apiErrorToast(result.error, 'Failed to load the version');
				version = null;
				return;
			}
			detail = result.data;
		});
		return () => {
			cancelled = true;
		};
	});

	function onOpenChange(open: boolean) {
		if (!open) version = null;
	}

	let content = $state<HTMLElement | null>(null);

	// The first focusable element is a relative time, whose exact-time tooltip would pop open on its own
	// Focusing the sheet itself still moves focus into it for screen readers, and Tab goes on to its first control
	function focusSheet(event: Event) {
		event.preventDefault();
		content?.focus();
	}
</script>

<Sheet.Root open={version !== null} {onOpenChange}>
	<Sheet.Content
		bind:ref={content}
		tabindex={-1}
		class="outline-none data-[side=right]:sm:max-w-3xl"
		onOpenAutoFocus={focusSheet}
	>
		<Sheet.Header>
			<Sheet.Title>Version {version}</Sheet.Title>
			<Sheet.Description>
				{#if detail}
					<VersionMeta
						author={detail.author}
						createdAt={detail.createdAt}
						sourceRunId={detail.sourceRunId}
					/>
				{:else}
					Changes compared to the version before
				{/if}
			</Sheet.Description>
		</Sheet.Header>
		<Sheet.Body>
			{#if loading}
				<Skeleton class="h-5 w-48" />
				<Skeleton class="h-40 w-full" />
			{:else if detail}
				{#if detail.summary}
					<VersionSummary author={detail.author} summary={detail.summary} />
				{/if}
				<section class="flex flex-col gap-3">
					<div class="flex flex-wrap items-baseline justify-between gap-x-3">
						<h3 class="text-sm font-semibold">Changes</h3>
						{#if outcome}
							<p class="text-muted-foreground text-xs">{outcome}</p>
						{/if}
					</div>
					{#each changes as change, i (change.key)}
						<VersionChange {change} first={i === 0} />
					{:else}
						<p class="text-muted-foreground text-sm">No changes compared to the version before</p>
					{/each}
				</section>
			{/if}
		</Sheet.Body>
		{#if detail && version !== null && version !== currentVersion}
			<Sheet.Footer>
				<Button variant="outline" onclick={() => version !== null && onRollback(version)}>
					<RotateCcwIcon data-icon="inline-start" />
					Roll back to version {version}
				</Button>
			</Sheet.Footer>
		{/if}
	</Sheet.Content>
</Sheet.Root>
