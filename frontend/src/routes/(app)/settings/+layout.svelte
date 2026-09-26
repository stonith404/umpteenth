<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import PageHeader from '$lib/components/page-header.svelte';
	import * as Tabs from '$lib/components/ui/tabs';
	import { settingsTabs } from '$lib/navigation';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Each tab is its own route, so tabs are linkable and load only their own data
	const activeTab = $derived(
		settingsTabs.find((tab) => page.url.pathname.startsWith(tab.href))?.value ??
			settingsTabs[0].value
	);

	function onTabChange(value: string) {
		const tab = settingsTabs.find((t) => t.value === value);
		if (tab) void goto(tab.href);
	}
</script>

<svelte:head>
	<title>Settings · Umpteenth</title>
</svelte:head>

<PageHeader title="Settings" description="Workspace defaults, limits and access for automation." />

<Tabs.Root value={activeTab} onValueChange={onTabChange} class="gap-6">
	<Tabs.List variant="line">
		{#each settingsTabs as tab (tab.value)}
			<Tabs.Trigger value={tab.value}>{tab.label}</Tabs.Trigger>
		{/each}
	</Tabs.List>
	<Tabs.Content value={activeTab}>
		{@render children()}
	</Tabs.Content>
</Tabs.Root>
