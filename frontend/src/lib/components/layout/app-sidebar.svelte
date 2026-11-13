<script lang="ts">
	import { afterNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { User } from '#lib/api/types.js';
	import Logo from '#lib/components/logo.svelte';
	import Wordmark from '#lib/components/wordmark.svelte';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import { isNavItemActive, navGroups, secondaryNav, type NavItem } from '#lib/navigation.js';
	import RunService from '#lib/services/run-service.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import { subscribeWorkspaceEvents } from '#lib/utils/workspace-events.js';
	import { cn } from '#lib/utils/style.js';
	import CaretRightIcon from 'phosphor-svelte/lib/CaretRight';
	import MagnifyingGlassIcon from 'phosphor-svelte/lib/MagnifyingGlass';
	import { onMount } from 'svelte';
	import { commandPalette } from './command-palette.svelte';
	import WorkspaceSwitcher from './workspace-switcher.svelte';

	let { user }: { user: User } = $props();

	const sidebar = Sidebar.useSidebar();

	// The icon rail, where groups can't open and lead to their first page instead
	const rail = $derived(sidebar.state === 'collapsed' && !sidebar.isMobile);

	// Groups the user opened or closed, the rest stay open while one of their pages is showing
	let expanded = $state<Record<string, boolean>>({});

	// A sidebar covering the page gets out of the way of any new page, also one reached from the command palette or the browser's back button
	// Shallow navigations such as switching a tab only change the URL hash, so they leave the sidebar open
	afterNavigate(({ shallow }) => {
		if (!shallow) sidebar.dismiss();
	});

	// The palette opens with ⌘K on Apple devices and Ctrl+K elsewhere, so the hint names the key people actually press
	const shortcutLabel = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K';

	// Runs that are underway, while queued ones still wait for a sandbox
	const RUNNING_STATUSES = 'provisioning,running,verifying';
	// Status changes come in bursts, so the count reloads once per burst
	const RUNNING_REFRESH_DELAY_MS = 500;

	const runService = new RunService();
	let runningJobs = $state(0);

	// A job with several runs underway counts once, since the badge sits on the jobs link
	async function loadRunningJobs() {
		const result = await tryCatch(runService.list({ status: RUNNING_STATUSES, pageSize: 100 }));
		if (result.error) return;
		runningJobs = new Set(result.data.items.map((run) => run.jobId)).size;
	}

	// The count follows the workspace's live run events, and reloads after a dropped connection or a workspace switch
	onMount(() => {
		let timer: ReturnType<typeof setTimeout> | undefined;
		void loadRunningJobs();
		const unsubscribe = subscribeWorkspaceEvents({
			onRun: () => {
				clearTimeout(timer);
				timer = setTimeout(loadRunningJobs, RUNNING_REFRESH_DELAY_MS);
			},
			onReconnect: () => void loadRunningJobs()
		});
		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

{#snippet navMenu(items: NavItem[])}
	<Sidebar.Menu>
		{#each items as item (item.href)}
			{@const active = isNavItemActive(item.href, page.url.pathname)}
			<Sidebar.MenuItem>
				{#if item.children}
					{@const open = expanded[item.href] ?? active}
					<!-- A group opens in place like Cloudflare's, while in the rail, where its pages have no room, it leads to the first of them -->
					<Sidebar.MenuButton
						isActive={active && rail}
						tooltipContent={item.label}
						aria-expanded={rail ? undefined : open}
						onclick={() => {
							if (rail) void goto(item.children![0].href);
							else expanded[item.href] = !open;
						}}
					>
						<item.icon />
						<span>{item.label}</span>
						<CaretRightIcon
							weight="bold"
							class={cn(
								'ml-auto size-3! opacity-40 transition-transform duration-(--sidebar-duration) ease-(--sidebar-easing)',
								open && 'rotate-90'
							)}
						/>
					</Sidebar.MenuButton>
					<!-- The pages slide open by growing a grid row, so the items below glide down instead of jumping -->
					<div
						class={cn('transition-grid-rows grid', open ? 'grid-rows-open' : 'grid-rows-closed')}
						inert={!open}
					>
						<div class="min-h-0 overflow-hidden">
							<Sidebar.MenuSub>
								{#each item.children as child (child.href)}
									<Sidebar.MenuSubItem>
										<Sidebar.MenuSubButton
											href={child.href}
											isActive={isNavItemActive(child.href, page.url.pathname)}
											aria-current={isNavItemActive(child.href, page.url.pathname)
												? 'page'
												: undefined}
											onclick={() => sidebar.dismiss()}
										>
											<span>{child.label}</span>
										</Sidebar.MenuSubButton>
									</Sidebar.MenuSubItem>
								{/each}
							</Sidebar.MenuSub>
						</div>
					</div>
				{:else}
					<Sidebar.MenuButton
						isActive={active}
						tooltipContent={item.label}
						onclick={() => sidebar.dismiss()}
					>
						{#snippet child({ props })}
							<a href={item.href} aria-current={active ? 'page' : undefined} {...props}>
								<item.icon />
								<span>{item.label}</span>
								{#if item.href === '/jobs' && runningJobs > 0}
									<span
										class="text-muted-foreground ml-auto flex items-center gap-1.5 text-xs font-normal tabular-nums"
										aria-label={`${runningJobs} running`}
									>
										<!-- A still cell of the Signal mark rather than a pulse, so the sidebar stays calm while jobs run for minutes -->
										<span class="bg-brand size-1.5" aria-hidden="true"></span>
										{runningJobs}
									</span>
								{/if}
							</a>
						{/snippet}
					</Sidebar.MenuButton>
				{/if}
			</Sidebar.MenuItem>
		{/each}
	</Sidebar.Menu>
{/snippet}

<Sidebar.Root collapsible="icon">
	<!-- Cloudflare's top row: the logo, then the account switcher, lined up with the header's bottom border -->
	<!-- Without workspaces there is nothing to switch, so the wordmark takes the switcher's place -->
	<Sidebar.Header>
		<a
			href="/"
			class="focus-visible:ring-ring flex shrink-0 items-center gap-2.5 rounded-lg outline-none focus-visible:ring-2"
			aria-label="Umpteenth home"
		>
			<Logo class="size-8 shrink-0" />
			{#if !user.workspacesEnabled}
				<!-- The wordmark fades while the rail clips it, instead of vanishing before the sidebar has moved -->
				<Wordmark
					class="h-4 shrink-0 transition-opacity duration-(--sidebar-duration) ease-(--sidebar-easing) group-data-[collapsible=icon]:opacity-0"
				/>
			{/if}
		</a>
		{#if user.workspacesEnabled}
			<WorkspaceSwitcher {user} />
		{/if}
	</Sidebar.Header>
	<Sidebar.Content>
		<!-- Quick search opens the command palette like the Cloudflare dashboard's search box, and collapses into one more icon of the rail -->
		<Sidebar.Group detached>
			<Sidebar.Menu>
				<Sidebar.MenuItem>
					<!-- A little shorter than the links below, like the dashboard's search box, until it joins the rail's row of icons -->
					<Sidebar.MenuButton
						variant="outline"
						class="min-h-8 group-data-[collapsible=icon]:min-h-8.5"
						tooltipContent="Search"
						onclick={() => {
							sidebar.dismiss();
							commandPalette.open = true;
						}}
					>
						<MagnifyingGlassIcon />
						<span>Quick search…</span>
						<!-- Phones have no keyboard to press it on -->
						{#if !sidebar.isMobile}
							<kbd class="text-muted-foreground ml-auto font-sans text-xs">{shortcutLabel}</kbd>
						{/if}
					</Sidebar.MenuButton>
				</Sidebar.MenuItem>
			</Sidebar.Menu>
		</Sidebar.Group>
		<!-- The pages form the navigation landmark, while the workspace switcher and search above are tools rather than destinations -->
		<nav aria-label="Main" class="flex flex-col">
			{#each navGroups as group, i (i)}
				<Sidebar.Group>
					{#if group.label}
						<Sidebar.GroupLabel>{group.label}</Sidebar.GroupLabel>
					{/if}
					<Sidebar.GroupContent>
						{@render navMenu(group.items)}
					</Sidebar.GroupContent>
				</Sidebar.Group>
			{/each}
			<Sidebar.Separator />
			<Sidebar.Group>
				<Sidebar.GroupContent>
					{@render navMenu(secondaryNav(user))}
				</Sidebar.GroupContent>
			</Sidebar.Group>
		</nav>
	</Sidebar.Content>
	<!-- Cloudflare keeps the collapse toggle alone in a footer row, in the same place whether the sidebar is open or a rail -->
	<Sidebar.Footer>
		<Sidebar.Trigger />
	</Sidebar.Footer>
	<Sidebar.Rail />
</Sidebar.Root>
