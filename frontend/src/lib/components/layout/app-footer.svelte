<!-- Cloudflare's page footer: a 48px row of links split by short dividers, ending in the product and its version -->
<script lang="ts">
	import { DOCS_URL } from '#lib/navigation.js';
	import SystemService from '#lib/services/system-service.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import { onMount } from 'svelte';

	const links = [
		{ label: 'Documentation', href: DOCS_URL },
		{ label: 'GitHub', href: 'https://github.com/stonith404/umpteenth' },
		{ label: 'Release notes', href: 'https://github.com/stonith404/umpteenth/releases' }
	];

	let version = $state<string>();

	// The version is a footnote, so the footer simply leaves it out when the request fails
	onMount(async () => {
		const result = await tryCatch(new SystemService().info());
		if (!result.error) version = result.data.version;
	});
</script>

<footer
	class="flex min-h-12 shrink-0 flex-wrap items-center justify-center border-t px-4 py-2 text-base"
>
	{#each links as link (link.href)}
		<a href={link.href} target="_blank" rel="noopener" class="link-underline px-4">{link.label}</a>
		<span class="bg-border h-4 w-px" aria-hidden="true"></span>
	{/each}
	<span class="text-muted-foreground px-4">Umpteenth{version ? ` ${version}` : ''}</span>
</footer>
