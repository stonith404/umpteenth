<script lang="ts">
	import { page } from '$app/state';
	import Logo from '$lib/components/logo.svelte';
	import Wordmark from '$lib/components/wordmark.svelte';
	import * as Sidebar from '$lib/components/ui/sidebar';
	import { isNavItemActive, navGroups, secondaryNav, type NavItem } from '$lib/navigation';
	import SearchIcon from '@lucide/svelte/icons/search';
	import { commandPalette } from './command-palette.svelte';

	const sidebar = Sidebar.useSidebar();
</script>

{#snippet navMenu(items: NavItem[])}
	<Sidebar.Menu>
		{#each items as item (item.href)}
			<Sidebar.MenuItem>
				<Sidebar.MenuButton
					isActive={isNavItemActive(item.href, page.url.pathname)}
					tooltipContent={item.label}
					onclick={() => sidebar.setOpenMobile(false)}
				>
					{#snippet child({ props })}
						<a href={item.href} {...props}>
							<item.icon />
							<span>{item.label}</span>
						</a>
					{/snippet}
				</Sidebar.MenuButton>
			</Sidebar.MenuItem>
		{/each}
	</Sidebar.Menu>
{/snippet}

<Sidebar.Root collapsible="icon">
	<!-- The brand block matches the header's height, so both borders form one line -->
	<Sidebar.Header>
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
							sidebar.setOpenMobile(false);
							commandPalette.open = true;
						}}
					>
						<SearchIcon />
						<span>Quick search…</span>
						<kbd class="text-muted-foreground ml-auto font-sans text-xs">⌘K</kbd>
					</Sidebar.MenuButton>
				</Sidebar.MenuItem>
			</Sidebar.Menu>
		</Sidebar.Group>
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
				{@render navMenu(secondaryNav)}
			</Sidebar.GroupContent>
		</Sidebar.Group>
	</Sidebar.Content>
	<Sidebar.Footer>
		<Sidebar.Trigger />
	</Sidebar.Footer>
	<Sidebar.Rail />
</Sidebar.Root>
