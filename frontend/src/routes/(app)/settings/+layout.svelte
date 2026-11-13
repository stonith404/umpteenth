<script lang="ts">
	import { page } from '$app/state';
	import PageHeader from '#lib/components/page-header.svelte';
	import { activePageTab } from '#lib/components/page-tabs.svelte';
	import { settingsTabs } from '#lib/navigation.js';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	const activeTab = $derived(activePageTab(settingsTabs, page.url.pathname) ?? settingsTabs[0]);
</script>

<!-- Names the tab first, so browser tabs and history entries of the sections can be told apart -->
<svelte:head>
	<title>{activeTab.label} · Settings · Umpteenth</title>
</svelte:head>

<!-- The sidebar lists the sections like Cloudflare's Manage account group, so each page is titled after its own section -->
<PageHeader
	title={activeTab.label}
	description={activeTab.description}
	docsPath={'docsPath' in activeTab ? activeTab.docsPath : undefined}
/>

{@render children()}
