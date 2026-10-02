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
		<main
			id="content"
			tabindex="-1"
			class="mx-auto flex w-full max-w-350 flex-1 flex-col gap-6 p-4 outline-none md:p-8 lg:px-10"
		>
			{@render children()}
		</main>
	</Sidebar.Inset>
</Sidebar.Provider>

<CommandPalette user={data.user} />
