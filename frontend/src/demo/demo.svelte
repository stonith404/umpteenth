<!--
@component
The app's own route tree for one run page: the root layout, the signed-in layout with its sidebar and header, and the run page, fed by the director instead of SvelteKit's router.
-->
<script lang="ts">
	import RootLayout from '../routes/+layout.svelte';
	import AppLayout from '../routes/(app)/+layout.svelte';
	import RunPage from '../routes/(app)/runs/[id]/+page.svelte';
	import { onMount } from 'svelte';
	import { director } from './director.svelte';
	import { user } from './fixtures';

	// The sidebar and the app's top bar lead to pages the demo doesn't have, so they are shown but take no clicks or focus
	onMount(() => {
		for (const region of document.querySelectorAll<HTMLElement>(
			'[data-slot="sidebar"], [data-slot="sidebar-inset"] > header'
		))
			region.inert = true;
	});
</script>

<RootLayout>
	<AppLayout data={{ user }}>
		{#if director.run}
			<!-- A fresh replay of the run on screen has the same ID, so the counter remounts the page for it -->
			{#key director.mount}
				<RunPage data={{ user, run: director.run, breadcrumbLabels: {} }} />
			{/key}
		{/if}
	</AppLayout>
</RootLayout>
