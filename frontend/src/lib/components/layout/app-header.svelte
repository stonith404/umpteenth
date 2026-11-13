<script lang="ts">
	import { afterNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import type { User } from '#lib/api/types.js';
	import * as Breadcrumb from '#lib/components/ui/breadcrumb/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import { buildBreadcrumbs, buildErrorBreadcrumbs } from '#lib/utils/breadcrumb-util.js';
	import { errorPageContent } from '#lib/utils/error-util.js';
	import { cn } from '#lib/utils/style.js';
	import { DOCS_URL, mainNav, secondaryNav } from '#lib/navigation.js';
	import BookOpenIcon from 'phosphor-svelte/lib/BookOpen';
	import MagnifyingGlassIcon from 'phosphor-svelte/lib/MagnifyingGlass';
	import { onMount } from 'svelte';
	import { commandPalette } from './command-palette.svelte';
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

	// The first crumb links to a section of the sidebar and carries its icon, like the product a dashboard trail starts at
	const sectionIcon = $derived(
		[...mainNav, ...secondaryNav(user)].find((item) => item.href === breadcrumbs[0]?.href)?.icon
	);

	// A page at the top of a section names itself in its h1, so its one-crumb trail only fades in once the h1 has scrolled under the header
	// It starts out hidden, since a page opens at its top with the h1 in view, and the observer's first report corrects that right away when it doesn't
	let titleInView = $state(true);
	const trailHidden = $derived(breadcrumbs.length === 1 && titleInView);

	// Each page brings its own h1, so the observer moves to the new one after every navigation
	let observer: IntersectionObserver | undefined;
	function watchTitle() {
		observer?.disconnect();
		const title = document.querySelector('main h1');
		// A page without an h1 has nothing else naming it, so its trail stays in view
		titleInView = !!title;
		if (!title) return;
		observer = new IntersectionObserver(([entry]) => (titleInView = entry.isIntersecting), {
			rootMargin: '-58px 0px 0px 0px'
		});
		observer.observe(title);
	}

	afterNavigate(watchTitle);
	onMount(() => () => observer?.disconnect());
</script>

<!-- Cloudflare's header: on the same canvas as the sidebar and the page, 58px tall to line up with the sidebar's top row, with 16px gutters -->
<header class="bg-background sticky top-0 z-10 h-14.5 shrink-0 border-b">
	<div class="flex h-full w-full items-center gap-2 px-4">
		<!-- On desktop and tablets the sidebar has its own collapse toggle in its header -->
		<Sidebar.Trigger class="-ml-1 md:hidden" />
		<Breadcrumb.Root class="min-w-0 flex-1" concealed={trailHidden}>
			<Breadcrumb.List>
				{#each breadcrumbs as crumb, i (i)}
					<!-- A separator goes with the crumb before it, so on phones the trail starts at the parent without a leading separator -->
					{#if i > 0}
						<Breadcrumb.Separator class={cn(hiddenOnMobile(i - 1) && 'max-sm:hidden')} />
					{/if}
					{#if crumb.href}
						<!-- Ancestors keep their width and only the current page truncates, but a long job name is capped so it can't push the current page out -->
						<!-- On phones the cap is a share of the trail, which leaves a short current page like 'Settings' its full width -->
						<Breadcrumb.Item
							class={cn('shrink-0 max-sm:max-w-9/20', hiddenOnMobile(i) && 'max-sm:hidden')}
						>
							<Breadcrumb.Link href={crumb.href} title={crumb.label} class="sm:max-w-64">
								<span class="flex min-w-0 items-center gap-1.5">
									{#if i === 0 && sectionIcon}
										{@const Icon = sectionIcon}
										<Icon class="size-4 shrink-0" aria-hidden="true" />
									{/if}
									<span class="truncate">{crumb.label}</span>
								</span>
							</Breadcrumb.Link>
						</Breadcrumb.Item>
					{:else}
						<Breadcrumb.Item>
							<Breadcrumb.Page title={crumb.label}>{crumb.label}</Breadcrumb.Page>
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
				<MagnifyingGlassIcon />
			</Button>
			<!-- Labelled ghost buttons like the dashboard's Ask AI and Support, which shrink to their icon on phones -->
			<Button
				variant="ghost"
				size="sm"
				href={DOCS_URL}
				target="_blank"
				rel="noopener"
				class="max-sm:hidden"
			>
				<BookOpenIcon data-icon="inline-start" />
				Docs
			</Button>
			<UserMenu {user} />
		</div>
	</div>
	<NavigationProgress />
</header>
