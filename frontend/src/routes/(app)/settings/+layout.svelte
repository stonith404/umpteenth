<script lang="ts">
	import { page } from '$app/state';
	import PageHeader from '$lib/components/page-header.svelte';
	import PageTabs, { activePageTab } from '$lib/components/page-tabs.svelte';
	import { settingsTabs } from '$lib/navigation';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Each tab is its own route, so tabs are linkable and load only their own data
	const activeTab = $derived(activePageTab(settingsTabs, page.url.pathname) ?? settingsTabs[0]);
</script>

<!-- Names the tab first, so browser tabs and history entries of the sections can be told apart -->
<svelte:head>
	<title>{activeTab.label} · Settings · Umpteenth</title>
</svelte:head>

<PageHeader title="Settings" description="Workspace defaults, limits and access for automation." />

<PageTabs tabs={settingsTabs} label="Settings sections">
	{@render children()}
</PageTabs>
