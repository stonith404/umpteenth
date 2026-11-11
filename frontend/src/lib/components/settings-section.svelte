<!--
	A section of a settings page laid out like Cloudflare's: a 16px heading and its description above a 12px card
	The section is named after its heading in the URL too, so a table of contents can link to it
	Usage: <SettingsSection title="Webhook" description="…">…</SettingsSection>
-->
<script lang="ts">
	import { anchorId } from '#lib/components/table-of-contents.svelte';
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		actions,
		titleId: titleIdProp,
		children
	}: {
		title: string;
		description?: string | Snippet;
		// Controls beside the heading, e.g. 'Send test' or a status badge
		actions?: Snippet;
		// The heading's ID, for a form inside the card that is named after it
		titleId?: string;
		children: Snippet;
	} = $props();

	const ownId = $props.id();
	const titleId = $derived(titleIdProp ?? ownId);
</script>

<section id={anchorId(title)} aria-labelledby={titleId} class="flex scroll-mt-20 flex-col gap-3">
	<header class="grid-cols-trailing-action grid items-start gap-x-4">
		<div class="flex min-w-0 flex-col gap-1">
			<h2 id={titleId} class="text-lg leading-snug font-medium">{title}</h2>
			{#if typeof description === 'string'}
				<p class="text-muted-foreground text-sm leading-snug">{description}</p>
			{:else if description}
				<p class="text-muted-foreground text-sm leading-snug">{@render description()}</p>
			{/if}
		</div>
		{#if actions}
			<div class="flex items-center gap-2">{@render actions()}</div>
		{/if}
	</header>
	<div class="bg-card ring-border rounded-xl p-5 shadow-xs ring-1">
		{@render children()}
	</div>
</section>
