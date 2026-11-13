<!--
	A section of a settings page laid out like Cloudflare's: one white card with the 16px heading and its description at the top
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

<section
	id={anchorId(title)}
	aria-labelledby={titleId}
	class="bg-card ring-border flex scroll-mt-20 flex-col gap-4 rounded-lg p-6 shadow-xs ring-1"
>
	<header class="grid-cols-trailing-action grid items-start gap-x-4">
		<div class="flex min-w-0 flex-col gap-2">
			<h2 id={titleId} class="text-lg font-semibold">{title}</h2>
			{#if typeof description === 'string'}
				<p class="text-muted-foreground text-base">{description}</p>
			{:else if description}
				<p class="text-muted-foreground text-base">{@render description()}</p>
			{/if}
		</div>
		{#if actions}
			<div class="flex items-center gap-2">{@render actions()}</div>
		{/if}
	</header>
	{@render children()}
</section>
