<script lang="ts">
	import { afterNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import type { User } from '$lib/api/types';
	import Logo from '$lib/components/logo.svelte';
	import Wordmark from '$lib/components/wordmark.svelte';
	import * as Sidebar from '$lib/components/ui/sidebar';
	import { isNavItemActive, navGroups, secondaryNav, type NavItem } from '$lib/navigation';
	import RunService from '$lib/services/run-service';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { subscribeWorkspaceEvents } from '$lib/utils/workspace-events';
	import SearchIcon from '@lucide/svelte/icons/search';
	import { cn } from '$lib/utils/style';
	import { onMount } from 'svelte';
	import { commandPalette } from './command-palette.svelte';
	import WorkspaceSwitcher from './workspace-switcher.svelte';

	let { user }: { user: User } = $props();

	const sidebar = Sidebar.useSidebar();

	// A sidebar covering the page gets out of the way of any new page, also one reached from the command palette or the browser's back button
	afterNavigate(() => sidebar.dismiss());

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
			<Sidebar.MenuItem>
				<Sidebar.MenuButton
					isActive={isNavItemActive(item.href, page.url.pathname)}
					tooltipContent={item.label}
					onclick={() => sidebar.dismiss()}
				>
					{#snippet child({ props })}
						<a href={item.href} {...props}>
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
			</Sidebar.MenuItem>
		{/each}
	</Sidebar.Menu>
{/snippet}

<Sidebar.Root collapsible="icon">
	<!-- The brand block matches the header's height, so both borders form one line -->
	<Sidebar.Header class="relative">
		<a
			href="/"
			class="focus-visible:ring-ring flex items-center gap-2.5 rounded-lg outline-none focus-visible:ring-2"
			aria-label="Umpteenth home"
		>
			<Logo class="size-8 shrink-0" />
			<!-- The wordmark fades while the rail clips it, instead of vanishing before the sidebar has moved -->
			<Wordmark
				class="h-4 shrink-0 transition-opacity duration-(--sidebar-duration) ease-(--sidebar-easing) group-data-[collapsible=icon]:opacity-0"
			/>
		</a>
		<!-- The expanded sidebar collapses from beside the logo, while the rail has its toggle in the footer since there is no room for both up here -->
		<Sidebar.Trigger
			class="absolute top-1/2 right-3 -translate-y-1/2 transition-[opacity,visibility] duration-(--sidebar-duration) ease-(--sidebar-easing) group-data-[collapsible=icon]:invisible group-data-[collapsible=icon]:opacity-0"
		/>
	</Sidebar.Header>
	<Sidebar.Content>
		<!-- Quick search opens the command palette like the Cloudflare dashboard's search box, and collapses into one more icon of the rail -->
		<Sidebar.Group
			class="mb-3 transition-[margin] duration-(--sidebar-duration) ease-(--sidebar-easing) group-data-[collapsible=icon]:mb-px"
		>
			<Sidebar.Menu>
				<Sidebar.MenuItem>
					<Sidebar.MenuButton
						variant="outline"
						tooltipContent="Search"
						class="font-normal"
						onclick={() => {
							sidebar.dismiss();
							commandPalette.open = true;
						}}
					>
						<SearchIcon />
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
	<!-- The workspace switcher gives way to the expand toggle in the rail, crossfading in place so the footer keeps its height -->
	<!-- Without workspaces the footer only holds the toggle, so its border shows in the rail alone -->
	<Sidebar.Footer
		class={cn(
			'relative h-auto min-h-12 px-3.5 py-2 transition-[padding,border-color]',
			!user.workspacesEnabled && 'border-transparent group-data-[collapsible=icon]:border-border'
		)}
	>
		{#if user.workspacesEnabled}
			<WorkspaceSwitcher {user} />
		{/if}
		<Sidebar.Trigger
			class="invisible absolute top-1/2 left-[11px] -translate-y-1/2 opacity-0 transition-[opacity,visibility] duration-(--sidebar-duration) ease-(--sidebar-easing) group-data-[collapsible=icon]:visible group-data-[collapsible=icon]:opacity-100"
		/>
	</Sidebar.Footer>
	<Sidebar.Rail />
</Sidebar.Root>
