<script lang="ts">
	import { page } from '$app/state';
	import type { User } from '$lib/api/types';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb';
	import { Button } from '$lib/components/ui/button';
	import * as Sidebar from '$lib/components/ui/sidebar';
	import { buildBreadcrumbs, buildErrorBreadcrumbs } from '$lib/utils/breadcrumb-util';
	import { errorPageContent } from '$lib/utils/error-util';
	import { cn } from '$lib/utils/style';
	import SearchIcon from '@lucide/svelte/icons/search';
	import { commandPalette } from './command-palette.svelte';
	import ModeSwitcher from './mode-switcher.svelte';
	import NavigationProgress from './navigation-progress.svelte';
	import UserMenu from './user-menu.svelte';

	let { user }: { user: User } = $props();

	// A failed load leaves no labels for the page's IDs, so an error page ends the trail in its own title instead of a raw ID
	const breadcrumbs = $derived(
		page.error
			? buildErrorBreadcrumbs(
					page.url.pathname,
					errorPageContent(page.error, page.status, page.route.id, page.url.pathname),
					page.data.breadcrumbLabels
				)
			: buildBreadcrumbs(page.url.pathname, page.data.breadcrumbLabels)
	);

	// Phones show only the parent and the current page, since the page's h1 carries the full name anyway
	// Unlike Kumo there is no leading ellipsis, because in a header shared with three buttons its width is what the current page needs
	const hiddenOnMobile = (i: number) => i < breadcrumbs.length - 2;
</script>

<header class="bg-card sticky top-0 z-10 h-14.5 shrink-0 border-b">
	<!-- The same width and gutters as the page below, so the first crumb starts at the h1 and the avatar ends at the content's right edge -->
	<div class="mx-auto flex h-full w-full max-w-[1400px] items-center gap-2 px-4 md:px-8 lg:px-10">
		<!-- On desktop and tablets the sidebar has its own collapse toggle in its header -->
		<Sidebar.Trigger class="-ml-1 md:hidden" />
		<Breadcrumb.Root class="min-w-0 flex-1">
			<Breadcrumb.List class="text-base">
				{#each breadcrumbs as crumb, i (i)}
					<!-- A separator goes with the crumb before it, so on phones the trail starts at the parent without a leading separator -->
					{#if i > 0}
						<Breadcrumb.Separator class={cn(hiddenOnMobile(i - 1) && 'max-sm:hidden')} />
					{/if}
					{#if crumb.href}
						<!-- Ancestors keep their width and only the current page truncates, but a long job name is capped so it can't push the current page out -->
						<!-- On phones the cap is a share of the trail, which leaves a short current page like 'Settings' its full width -->
						<Breadcrumb.Item
							class={cn('shrink-0 max-sm:max-w-[45%]', hiddenOnMobile(i) && 'max-sm:hidden')}
						>
							<Breadcrumb.Link
								href={crumb.href}
								title={crumb.label}
								class="truncate sm:max-w-[16rem]"
							>
								{crumb.label}
							</Breadcrumb.Link>
						</Breadcrumb.Item>
					{:else}
						<Breadcrumb.Item>
							<Breadcrumb.Page class="font-medium" title={crumb.label}
								>{crumb.label}</Breadcrumb.Page
							>
						</Breadcrumb.Item>
					{/if}
				{/each}
			</Breadcrumb.List>
		</Breadcrumb.Root>
		<div class="flex shrink-0 items-center gap-1">
			<Button
				variant="ghost"
				size="icon-sm"
				class="md:hidden"
				aria-label="Search"
				onclick={() => (commandPalette.open = true)}
			>
				<SearchIcon />
			</Button>
			<ModeSwitcher />
			<UserMenu {user} />
		</div>
	</div>
	<NavigationProgress />
</header>
