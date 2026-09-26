<script lang="ts">
	import { page } from '$app/state';
	import PageHeader from '$lib/components/page-header.svelte';
	import PageTabs, { activePageTab } from '$lib/components/page-tabs.svelte';
	import { adminTabs } from '$lib/navigation';
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

<PageHeader
	title="Admin"
	description="Everyone who signed in to this instance, and every workspace."
/>

<PageTabs {tabs} label="Admin sections">
	{@render children()}
</PageTabs>
