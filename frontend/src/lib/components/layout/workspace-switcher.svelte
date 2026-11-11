<script lang="ts">
	import type { User, Workspace } from '#lib/api/types.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import * as Sidebar from '#lib/components/ui/sidebar/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import CreateWorkspaceDialog from '#lib/components/workspaces/create-workspace-dialog.svelte';
	import WorkspaceService from '#lib/services/workspace-service.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import { enterWorkspace, roleLabels, workspaceInitial } from '#lib/utils/workspace-util.js';
	import CheckIcon from 'phosphor-svelte/lib/Check';
	import CaretUpDownIcon from 'phosphor-svelte/lib/CaretUpDown';
	import PlusIcon from 'phosphor-svelte/lib/Plus';
	import MagnifyingGlassIcon from 'phosphor-svelte/lib/MagnifyingGlass';

	let { user }: { user: User } = $props();

	const workspaceService = new WorkspaceService();
	const sidebar = Sidebar.useSidebar();

	// Past this many workspaces the menu gets a search field, since truncated names that start alike are hard to tell apart
	const SEARCH_THRESHOLD = 8;

	let workspaces = $state<Workspace[] | null>(null);
	let createOpen = $state(false);
	let switching = $state(false);
	let filter = $state('');

	const searchable = $derived((workspaces?.length ?? 0) > SEARCH_THRESHOLD);
	const visibleWorkspaces = $derived.by(() => {
		const q = filter.trim().toLowerCase();
		if (!workspaces || !q) return workspaces;
		return workspaces.filter((workspace) => workspace.name.toLowerCase().includes(q));
	});

	// The list is loaded whenever the menu opens, so workspaces joined in another tab show up
	async function load() {
		const result = await tryCatch(workspaceService.listMine());
		if (result.error) {
			apiErrorToast(result.error, 'Failed to load your workspaces');
			return;
		}
		workspaces = result.data;
	}

	// A searchable list starts in its search field, so typing filters right away
	// The list loads after the menu opened the first time, so the field focuses itself whenever it appears rather than when the menu opens
	function focusOnMount(node: HTMLInputElement) {
		node.focus();
	}

	function onOpenChange(isOpen: boolean) {
		if (!isOpen) return;
		filter = '';
		load();
	}

	// The search field keeps the letters it is typed, which the menu would otherwise take for jumping to an item
	// Arrow down moves on to the first workspace, so the list stays reachable from the keyboard
	function onSearchKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape' || e.key === 'Tab') return;
		e.stopPropagation();
		if (e.key !== 'ArrowDown') return;
		e.preventDefault();
		const menu = (e.currentTarget as HTMLElement).closest('[data-slot="dropdown-menu-content"]');
		menu
			?.querySelector<HTMLElement>('[data-slot="dropdown-menu-item"]:not([data-disabled])')
			?.focus();
	}

	async function switchTo(workspace: Workspace) {
		if (workspace.id === user.workspace.id) return;

		switching = true;
		const result = await tryCatch(workspaceService.switchTo(workspace.id));
		if (result.error) {
			switching = false;
			apiErrorToast(result.error, 'Failed to switch workspaces');
			return;
		}
		sidebar.dismiss();
		await enterWorkspace();
		switching = false;
	}
</script>

<!-- Cloudflare's account switcher: the name beside the logo in the sidebar's top row, which fades out in the rail where only the logo fits -->
<DropdownMenu.Root {onOpenChange}>
	<DropdownMenu.Trigger>
		{#snippet child({ props })}
			<button
				{...props}
				type="button"
				aria-label={`Workspace ${user.workspace.name}, switch workspaces`}
				class="hover:bg-sidebar-accent data-[state=open]:bg-sidebar-accent focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-1 rounded-lg px-2 py-1.5 text-base font-medium outline-none focus-visible:ring-2 transition-rail-fade group-data-[collapsible=icon]:invisible group-data-[collapsible=icon]:opacity-0"
			>
				<span class="truncate">{user.workspace.name}</span>
				<!-- The role stays in the text for screen readers and the menu's label, while the row shows only the name like Cloudflare's account switcher -->
				<span class="sr-only">{roleLabels[user.workspace.role]}</span>
				{#if switching}
					<Spinner class="ml-auto" />
				{:else}
					<CaretUpDownIcon class="text-muted-foreground ml-auto" />
				{/if}
			</button>
		{/snippet}
	</DropdownMenu.Trigger>
	<DropdownMenu.Content
		long
		class="w-60"
		side="bottom"
		align="start"
		onOpenAutoFocus={(e) => {
			// The search field takes the focus itself, see focusOnMount
			if (searchable) e.preventDefault();
		}}
	>
		{#if searchable}
			<div class="relative mb-1">
				<MagnifyingGlassIcon
					class="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2"
				/>
				<input
					{@attach focusOnMount}
					bind:value={filter}
					type="search"
					placeholder="Find a workspace…"
					aria-label="Find a workspace"
					autocomplete="off"
					class="placeholder:text-muted-foreground h-8 w-full rounded-md bg-transparent pr-2 pl-8 text-base outline-none"
					onkeydown={onSearchKeydown}
				/>
			</div>
			<DropdownMenu.Separator />
		{/if}
		<DropdownMenu.Label size="sm">Workspaces</DropdownMenu.Label>
		{#if visibleWorkspaces === null}
			<div class="flex justify-center py-3"><Spinner /></div>
		{:else}
			{#each visibleWorkspaces as workspace (workspace.id)}
				<!-- Long names that start alike truncate to the same text, so the full name shows on hover -->
				<DropdownMenu.Item onSelect={() => switchTo(workspace)} title={workspace.name}>
					<!-- The margins widen the item's gap by 2px, so the tile and the check stand a little clear of the name -->
					<span
						class="bg-fill text-fill-foreground mr-0.5 flex size-6 shrink-0 items-center justify-center rounded-md text-xs font-semibold"
						aria-hidden="true"
					>
						{workspaceInitial(workspace.name)}
					</span>
					<span class="min-w-0 flex-1 truncate">{workspace.name}</span>
					{#if workspace.id === user.workspace.id}
						<CheckIcon class="ml-0.5" aria-hidden="false" aria-label="Current workspace" />
					{/if}
				</DropdownMenu.Item>
			{:else}
				<p class="text-muted-foreground px-2 py-1.5 text-sm">
					No workspace matches "{filter.trim()}"
				</p>
			{/each}
		{/if}
		<DropdownMenu.Separator />
		<DropdownMenu.Item onSelect={() => (createOpen = true)}>
			<PlusIcon />
			Create workspace
		</DropdownMenu.Item>
	</DropdownMenu.Content>
</DropdownMenu.Root>

<CreateWorkspaceDialog bind:open={createOpen} />
