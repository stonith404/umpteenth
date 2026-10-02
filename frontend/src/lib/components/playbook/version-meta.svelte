<script lang="ts">
	import AuthorLabel from '#lib/components/playbook/author-label.svelte';
	import RelativeTime from '#lib/components/relative-time.svelte';
	import ArrowUpRightIcon from '@lucide/svelte/icons/arrow-up-right';
	import ClockIcon from '@lucide/svelte/icons/clock';

	let {
		author,
		createdAt,
		sourceRunId
	}: {
		author: string;
		createdAt?: number | null;
		sourceRunId?: string | null;
	} = $props();
</script>

<!-- Who made a version, when, and the run it came from, as one wrapping row -->
<span class="flex flex-wrap items-center gap-x-4 gap-y-1">
	<AuthorLabel {author} />
	{#if createdAt}
		<span class="inline-flex items-center gap-1.5">
			<ClockIcon class="size-3.5" aria-hidden="true" />
			<RelativeTime value={createdAt} />
		</span>
	{/if}
	{#if sourceRunId}
		<a
			href="/runs/{sourceRunId}"
			class="text-foreground inline-flex items-center gap-1 link-underline"
		>
			Source run
			<ArrowUpRightIcon class="size-3.5" aria-hidden="true" />
		</a>
	{/if}
</span>
