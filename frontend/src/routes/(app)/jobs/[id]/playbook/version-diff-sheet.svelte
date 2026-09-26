<script lang="ts">
	import type { PlaybookVersion } from '$lib/api/types';
	import DiffView from '$lib/components/diff-view.svelte';
	import OpsList from '$lib/components/playbook/ops-list.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Sheet from '$lib/components/ui/sheet';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import PlaybookService from '$lib/services/playbook-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { authorLabel, changedSections } from '$lib/utils/playbook-util';
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

	const sections = $derived(detail ? changedSections(detail.previous, detail.content) : []);

	// The version endpoint includes the previous version's content, so one request gives the whole diff
	$effect(() => {
		const v = version;
		if (v === null) return;
		detail = null;
		loading = true;
		void tryCatch(playbookService.version(jobId, v)).then((result) => {
			if (version !== v) return;
			loading = false;
			if (result.error) {
				apiErrorToast(result.error, 'Failed to load the version');
				version = null;
				return;
			}
			detail = result.data;
		});
	});

	function onOpenChange(open: boolean) {
		if (!open) version = null;
	}
</script>

<Sheet.Root open={version !== null} {onOpenChange}>
	<Sheet.Content class="w-full data-[side=right]:sm:max-w-3xl">
		<Sheet.Header>
			<Sheet.Title>Version {version}</Sheet.Title>
			<Sheet.Description>
				{#if detail}
					<span class="inline-flex flex-wrap items-center gap-2">
						<Badge variant="secondary">{authorLabel(detail.author)}</Badge>
						{#if detail.createdAt}<RelativeTime value={detail.createdAt} />{/if}
						{#if detail.sourceRunId}
							· <a href="/runs/{detail.sourceRunId}" class="underline underline-offset-3"
								>source run</a
							>
						{/if}
					</span>
				{:else}
					Changes compared to the version before
				{/if}
			</Sheet.Description>
		</Sheet.Header>
		<div class="flex flex-1 flex-col gap-6 overflow-y-auto px-4 pb-4">
			{#if loading}
				<Skeleton class="h-5 w-48" />
				<Skeleton class="h-40 w-full" />
			{:else if detail}
				{#if detail.summary}
					<p class="text-sm">{detail.summary}</p>
				{/if}
				{#if detail.ops && detail.ops.length > 0}
					<section class="flex flex-col gap-2">
						<h3 class="text-sm font-medium">What reflection proposed</h3>
						<OpsList ops={detail.ops} />
					</section>
				{/if}
				{#each sections as section (section.key)}
					<section class="flex flex-col gap-2">
						<h3 class="text-sm font-medium">{section.label}</h3>
						<DiffView before={section.before} after={section.after} />
					</section>
				{:else}
					<p class="text-muted-foreground text-sm">No changes compared to the version before.</p>
				{/each}
			{/if}
		</div>
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
