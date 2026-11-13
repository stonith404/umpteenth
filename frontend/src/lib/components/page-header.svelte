<!--
	The one page header, for list pages and detail pages alike, laid out like the dashboard's: a 20px title with a docs pill beside it, the description under it and the actions to the right
	Usage: <PageHeader title="Jobs" description="…" docsPath="/guides/jobs/">{#snippet actions()}<Button>Create job</Button>{/snippet}</PageHeader>
	Detail pages pass a `meta` snippet for a muted line of facts under the title
-->
<script lang="ts">
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { docsUrl } from '#lib/navigation.js';
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		docsPath,
		meta,
		actions
	}: {
		// The text of the page's only h1, or a snippet rendered inside it for titles with extra inline parts, e.g. a muted `#7`
		title: string | Snippet;
		// A sentence under the title, as text or a snippet for descriptions with links
		description?: string | Snippet;
		// The documentation page about this page, linked from a View docs pill beside the title, e.g. `/guides/jobs/`
		docsPath?: string;
		// A muted line of facts under the title and description, spanning the full width and wrapping onto more lines as it grows
		// Each fact is its own element, ideally a small icon followed by a few words, e.g. `<span class="inline-flex items-center gap-1.5"><ClockIcon class="size-4" /> Next run in 7 minutes</span>`
		meta?: Snippet;
		// Buttons to the right of the title and description from the sm breakpoint, and under them below it
		actions?: Snippet;
	} = $props();
</script>

<div data-slot="page-header" class="flex flex-col gap-2">
	<div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
		<div class="grid min-w-0 gap-2">
			<div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
				<!-- Long names wrap rather than truncate, so nothing that follows the name, like a run number, gets cut off -->
				<h1 class="min-w-0 text-xl font-semibold break-words">
					{#if typeof title === 'string'}
						{title}
					{:else}
						{@render title()}
					{/if}
				</h1>
				{#if docsPath}
					<Badge variant="outline" href={docsUrl(docsPath)} target="_blank" rel="noopener">
						View docs
					</Badge>
				{/if}
			</div>
			{#if description}
				<p class="text-muted-foreground max-w-187.5 text-base text-pretty">
					{#if typeof description === 'string'}
						{description}
					{:else}
						{@render description()}
					{/if}
				</p>
			{/if}
		</div>
		{#if actions}
			<!-- An actions snippet that renders nothing leaves no gap -->
			<div
				data-slot="page-header-actions"
				class="flex shrink-0 flex-wrap items-center gap-2.5 empty:hidden"
			>
				{@render actions()}
			</div>
		{/if}
	</div>
	{#if meta}
		<div
			data-slot="page-header-meta"
			class="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-sm"
		>
			{@render meta()}
		</div>
	{/if}
</div>
