<script lang="ts">
	import AppHeader from '#lib/components/layout/app-header.svelte';
	import AppSidebar from '#lib/components/layout/app-sidebar.svelte';
	import CommandPalette from '#lib/components/layout/command-palette.svelte';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import type { Snippet } from 'svelte';
	import type { LayoutData } from './$types';

	let { data, children }: { data: LayoutData; children: Snippet } = $props();
</script>

<!-- Keyboard users land on this first and can skip the sidebar's workspace switcher, search and links -->
<!-- It waits above the viewport rather than being sr-only, since undoing sr-only on focus would also undo its padding and position -->
<a
	href="#content"
	class="bg-popover text-popover-foreground ring-border focus-visible:ring-ring fixed top-2.5 left-2.5 z-50 -translate-y-20 rounded-lg px-3 py-2 text-sm font-medium shadow-lg ring-1 outline-none focus-visible:translate-y-0 focus-visible:ring-2"
>
	Skip to content
</a>

<Sidebar.Provider>
	<AppSidebar user={data.user} />
	<!-- Without min-w-0 the inset grows to fit its widest unbreakable content, e.g. a long URL in a run's output, and the whole page scrolls sideways -->
	<Sidebar.Inset class="min-w-0">
		<AppHeader user={data.user} />
		<!-- Pages sit in the dashboard's 1100px column, centered on the canvas with gutters of at least 16px or 32px -->
		<!-- The page is a size container, so a strip inside the column, like a job's tabs, can still run edge to edge -->
		<main
			id="content"
			tabindex="-1"
			class="@container flex w-full flex-1 flex-col gap-7 px-page py-8 outline-none *:mx-auto *:w-full *:max-w-page"
		>
			{@render children()}
		</main>
	</Sidebar.Inset>
</Sidebar.Provider>

<CommandPalette user={data.user} />
