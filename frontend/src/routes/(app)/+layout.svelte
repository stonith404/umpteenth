<script lang="ts">
	import AppHeader from '$lib/components/layout/app-header.svelte';
	import AppSidebar from '$lib/components/layout/app-sidebar.svelte';
	import CommandPalette from '$lib/components/layout/command-palette.svelte';
	import * as Sidebar from '$lib/components/ui/sidebar';
	import UnsavedChangesBar from '$lib/components/unsaved-changes-bar.svelte';
	import type { Snippet } from 'svelte';
	import type { LayoutData } from './$types';

	let { data, children }: { data: LayoutData; children: Snippet } = $props();
</script>

<Sidebar.Provider>
	<AppSidebar />
	<!-- Without min-w-0 the inset grows to fit its widest unbreakable content, e.g. a long URL in a run's output, and the whole page scrolls sideways -->
	<Sidebar.Inset class="min-w-0">
		<AppHeader user={data.user} />
		<main class="mx-auto flex w-full max-w-[1400px] flex-1 flex-col gap-6 p-4 md:p-8 lg:px-10">
			{@render children()}
		</main>
		<UnsavedChangesBar />
	</Sidebar.Inset>
</Sidebar.Provider>

<CommandPalette />
