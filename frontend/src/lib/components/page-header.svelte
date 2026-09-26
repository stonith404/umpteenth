<!--
	The one page header, for list pages and detail pages alike
	Usage: <PageHeader title="Jobs" description="…">{#snippet actions()}<Button>Create job</Button>{/snippet}</PageHeader>
	Detail pages pass snippets for the parts around the title, e.g. `eyebrow` for status badges and `meta` for a muted line of facts
-->
<script lang="ts">
	import { cn } from '$lib/utils/style';
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		eyebrow,
		meta,
		actions,
		class: className
	}: {
		// The text of the page's only h1, or a snippet rendered inside it for titles with extra inline parts, e.g. a muted `#7`
		title: string | Snippet;
		// A sentence under the title, as text or a snippet for descriptions with links
		description?: string | Snippet;
		// A row of badges above the title, e.g. a run's status and mode
		eyebrow?: Snippet;
		// A muted line of facts under the title and description, spanning the full width and wrapping onto more lines as it grows
		// Each fact is its own element, ideally a small icon followed by a few words, e.g. `<span class="inline-flex items-center gap-1.5"><ClockIcon class="size-4" /> Next run in 7 minutes</span>`
		meta?: Snippet;
		// Buttons pinned top right beside the title's first line from the sm breakpoint, and stacked under everything else below it
		actions?: Snippet;
		class?: string;
	} = $props();
</script>

<!-- From sm the title and the actions share the first row, and everything under the title spans both columns, so a long meta line wraps across the full width instead of squeezing beside the buttons -->
<div
	data-slot="page-header"
	class={cn('grid grid-cols-1 gap-y-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:gap-x-6', className)}
>
	{#if eyebrow}
		<div class="col-span-full flex flex-wrap items-center gap-2">
			{@render eyebrow()}
		</div>
	{/if}
	<!-- Long names wrap rather than truncate, so nothing that follows the name, like a run number, gets cut off -->
	<h1 class="min-w-0 text-2xl font-semibold break-words sm:text-3xl">
		{#if typeof title === 'string'}
			{title}
		{:else}
			{@render title()}
		{/if}
	</h1>
	{#if description}
		<p class="text-muted-foreground col-span-full max-w-prose text-base">
			{#if typeof description === 'string'}
				{description}
			{:else}
				{@render description()}
			{/if}
		</p>
	{/if}
	{#if meta}
		<div
			data-slot="page-header-meta"
			class="text-muted-foreground col-span-full flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-sm"
		>
			{@render meta()}
		</div>
	{/if}
	{#if actions}
		<!-- Placed in the title's row from sm and top aligned with it, since buttons and the title's line are both 36px tall -->
		<!-- On phones the actions come last, under the facts they act on, and an actions snippet that renders nothing leaves no gap -->
		<div
			data-slot="page-header-actions"
			class={cn(
				'mt-2 flex flex-wrap items-center gap-2 empty:hidden sm:col-start-2 sm:mt-0 sm:justify-end sm:self-start',
				eyebrow ? 'sm:row-start-2' : 'sm:row-start-1'
			)}
		>
			{@render actions()}
		</div>
	{/if}
</div>
