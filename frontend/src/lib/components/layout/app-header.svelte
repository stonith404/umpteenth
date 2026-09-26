<script lang="ts">
	import { page } from '$app/state';
	import type { User } from '$lib/api/types';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb';
	import * as Sidebar from '$lib/components/ui/sidebar';
	import { buildBreadcrumbs } from '$lib/utils/breadcrumb-util';
	import SearchIcon from '@lucide/svelte/icons/search';
	import { commandPalette } from './command-palette.svelte';
	import ModeSwitcher from './mode-switcher.svelte';
	import UserMenu from './user-menu.svelte';

	let { user }: { user: User } = $props();

	const breadcrumbs = $derived(
		buildBreadcrumbs(page.url.pathname, page.data.breadcrumbLabels as Record<string, string>)
	);
</script>

<header
	class="bg-card sticky top-0 z-10 flex h-14.5 shrink-0 items-center gap-2 border-b px-4 md:px-6"
>
	<!-- On desktop the sidebar has its own collapse toggle in its footer -->
	<Sidebar.Trigger class="-ml-1 md:hidden" />
	<Breadcrumb.Root>
		<Breadcrumb.List class="text-base">
			{#each breadcrumbs as crumb, i (i)}
				{#if i > 0}
					<Breadcrumb.Separator />
				{/if}
				<Breadcrumb.Item>
					{#if crumb.href}
						<Breadcrumb.Link href={crumb.href}>{crumb.label}</Breadcrumb.Link>
					{:else}
						<Breadcrumb.Page class="font-medium">{crumb.label}</Breadcrumb.Page>
					{/if}
				</Breadcrumb.Item>
			{/each}
		</Breadcrumb.List>
	</Breadcrumb.Root>
	<div class="ml-auto flex items-center gap-1">
		<button
			type="button"
			class="text-muted-foreground hover:text-foreground flex size-8 items-center justify-center md:hidden"
			aria-label="Search"
			onclick={() => (commandPalette.open = true)}
		>
			<SearchIcon class="size-4" />
		</button>
		<ModeSwitcher />
		<UserMenu {user} />
	</div>
</header>
