<script lang="ts">
	import { page } from '$app/state';
	import PageHeader from '#lib/components/page-header.svelte';
	import { activePageTab } from '#lib/components/page-tabs.svelte';
	import { adminTabs } from '#lib/navigation.js';
	import type { Snippet } from 'svelte';
	import type { LayoutData } from './$types';

	let { data, children }: { data: LayoutData; children: Snippet } = $props();

	const tabs = $derived(adminTabs(data.user));
	const activeTab = $derived(activePageTab(tabs, page.url.pathname) ?? tabs[0]);
</script>

<!-- Names the tab first, so the users and workspaces pages can be told apart in the browser -->
<svelte:head>
	<title>{activeTab.label} · Admin · Umpteenth</title>
</svelte:head>

<!-- The sidebar lists the sections like Cloudflare's Manage account group, so each page is titled after its own section -->
<PageHeader title={activeTab.label} description={activeTab.description} />

{@render children()}
