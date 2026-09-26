<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { McpServer, McpServerBody } from '$lib/api/types';
	import { openConfirmDialog } from '$lib/components/confirm-dialog';
	import { DataTable, renderSnippet } from '$lib/components/data-table';
	import PageHeader from '$lib/components/page-header.svelte';
	import RelativeTime from '$lib/components/relative-time.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import * as Empty from '$lib/components/ui/empty';
	import { Switch } from '$lib/components/ui/switch';
	import McpService from '$lib/services/mcp-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { canLogIn } from '$lib/utils/mcp-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
	import LogInIcon from '@lucide/svelte/icons/log-in';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlugZapIcon from '@lucide/svelte/icons/plug-zap';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import ServerIcon from '@lucide/svelte/icons/server';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import type { ColumnDef } from '@tanstack/table-core';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import AuthBadge from './auth-badge.svelte';
	import { startOAuthLogin } from './oauth-login';
	import ServerDialog from './server-dialog.svelte';
	import ServerSheet from './server-sheet.svelte';
	import ServerTestDialog from './server-test-dialog.svelte';

	const mcpService = new McpService();

	let dataTable: ReturnType<typeof DataTable<McpServer>> | undefined = $state();
	let dialogOpen = $state(false);
	let editing = $state<McpServer | null>(null);
	let testing = $state<McpServer | null>(null);
	let viewing = $state<McpServer | null>(null);
	let toggling = $state<Record<string, boolean>>({});

	const columns: ColumnDef<McpServer>[] = [
		{
			accessorKey: 'name',
			header: 'Name',
			meta: { sortKey: 'name', cellClass: 'max-w-64' },
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		},
		{
			accessorKey: 'transport',
			header: 'Transport',
			meta: { sortKey: 'transport' },
			cell: ({ row }) => renderSnippet(transportCell, row.original)
		},
		{
			id: 'auth',
			header: 'Auth',
			cell: ({ row }) => renderSnippet(authCell, row.original)
		},
		{
			id: 'target',
			header: 'Command or URL',
			meta: { cellClass: 'max-w-80' },
			cell: ({ row }) => renderSnippet(targetCell, row.original)
		},
		{
			accessorKey: 'toolsCachedAt',
			header: 'Tools',
			meta: { sortKey: 'toolsCachedAt' },
			cell: ({ row }) => renderSnippet(toolsCell, row.original)
		},
		{
			accessorKey: 'enabled',
			header: 'Enabled',
			meta: { headerClass: 'w-0', cellClass: 'w-0' },
			cell: ({ row }) => renderSnippet(enabledCell, row.original)
		},
		{
			id: 'actions',
			header: () => renderSnippet(srOnly, 'Actions'),
			meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
			cell: ({ row }) => renderSnippet(actionsCell, row.original)
		}
	];

	function openAdd() {
		editing = null;
		dialogOpen = true;
	}

	function openEdit(server: McpServer) {
		viewing = null;
		editing = server;
		dialogOpen = true;
	}

	function openTest(server: McpServer) {
		viewing = null;
		testing = server;
	}

	async function onSaved(server: McpServer, created: boolean) {
		toast.success(created ? `Added "${server.name}"` : `Saved "${server.name}"`);
		void dataTable?.refresh();
		if (!created) return;

		// A new server that advertises OAuth goes straight to its login, like codex mcp add does, and is tested once the browser comes back
		if (server.auth.status === 'not_logged_in') {
			toast.info(`"${server.name}" uses OAuth, opening its login…`);
			if (await startOAuthLogin(server)) return;
		}

		// Other new servers are tested right away, so their tools are known when attaching them to jobs
		testing = server;
	}

	async function logIn(server: McpServer) {
		viewing = null;
		testing = null;
		await startOAuthLogin(server);
	}

	function confirmLogout(server: McpServer) {
		viewing = null;
		openConfirmDialog({
			title: `Log out of ${server.name}`,
			message: 'Jobs that use this server lose access to it until someone logs in again.',
			confirm: {
				label: 'Log out',
				destructive: true,
				action: async () => {
					const result = await tryCatch(mcpService.logout(server.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to log out');
						return;
					}
					toast.success(`Logged out of "${server.name}"`);
					await dataTable?.refresh();
				}
			}
		});
	}

	// The OAuth callback sends the browser back here with the outcome, which is shown once and then dropped from the URL
	onMount(async () => {
		const params = page.url.searchParams;
		const serverId = params.get('server');
		const outcome = params.get('oauth');
		const error = params.get('oauthError');
		if (!serverId || (!outcome && error === null)) return;

		const url = new URL(page.url);
		for (const key of ['server', 'oauth', 'oauthError']) url.searchParams.delete(key);
		void goto(url, { replaceState: true, noScroll: true, keepFocus: true });

		const server = await tryCatch(mcpService.get(serverId));
		const name = server.data ? `"${server.data.name}"` : 'the server';
		if (error !== null) {
			toast.error(`Failed to log in to ${name}`, { description: error });
			return;
		}
		toast.success(`Logged in to ${name}`);

		// Testing right away lists the tools the login unlocked
		if (server.data) testing = server.data;
	});

	// The update endpoint replaces the whole server, so the switch sends every field back
	function bodyOf(server: McpServer): McpServerBody {
		return {
			name: server.name,
			description: server.description ?? undefined,
			transport: server.transport === 'http' ? 'http' : 'stdio',
			command: server.command ?? undefined,
			args: server.args ?? [],
			env: server.env,
			url: server.url ?? undefined,
			headers: server.headers,
			oauth: server.oauth,
			enabled: server.enabled
		};
	}

	async function setEnabled(server: McpServer, enabled: boolean) {
		toggling[server.id] = true;
		dataTable?.updateRow(server.id, { enabled });
		const result = await tryCatch(mcpService.update(server.id, { ...bodyOf(server), enabled }));
		toggling[server.id] = false;
		if (result.error) {
			dataTable?.updateRow(server.id, { enabled: !enabled });
			apiErrorToast(result.error, 'Failed to update the server');
			return;
		}
		dataTable?.updateRow(server.id, result.data);
	}

	function confirmDelete(server: McpServer) {
		openConfirmDialog({
			title: `Delete ${server.name}`,
			message: "Jobs that use this server lose access to its tools. This can't be undone.",
			confirm: {
				label: 'Delete',
				destructive: true,
				action: async () => {
					const result = await tryCatch(mcpService.delete(server.id));
					if (result.error) {
						apiErrorToast(result.error, 'Failed to delete the server');
						return;
					}
					toast.success(`Deleted "${server.name}"`);
					await dataTable?.refresh();
				}
			}
		});
	}

	// A finished test updated the cached tools, which the row and an open detail sheet show
	async function onTested() {
		await dataTable?.refresh();
	}
</script>

{#snippet srOnly(text: string)}
	<span class="sr-only">{text}</span>
{/snippet}

{#snippet nameCell(server: McpServer)}
	<button
		type="button"
		class="flex max-w-full flex-col items-start text-left"
		onclick={() => (viewing = server)}
	>
		<span class="truncate font-mono font-medium hover:underline">{server.name}</span>
		{#if server.description}
			<span class="text-muted-foreground max-w-full truncate text-xs">{server.description}</span>
		{/if}
	</button>
{/snippet}

{#snippet transportCell(server: McpServer)}
	<Badge variant="outline" class="font-normal"
		>{server.transport === 'http' ? 'HTTP' : 'stdio'}</Badge
	>
{/snippet}

{#snippet authCell(server: McpServer)}
	{#if server.auth.status === 'unsupported'}
		<span class="text-muted-foreground">None</span>
	{:else}
		<AuthBadge auth={server.auth} />
	{/if}
{/snippet}

{#snippet targetCell(server: McpServer)}
	{@const target =
		server.transport === 'http' ? server.url : [server.command, ...(server.args ?? [])].join(' ')}
	<span class="text-muted-foreground block truncate font-mono text-xs" title={target ?? undefined}>
		{target}
	</span>
{/snippet}

{#snippet toolsCell(server: McpServer)}
	{#if server.toolsCachedAt}
		<span class="flex flex-col">
			<span class="numeric">
				{server.tools?.length ?? 0}
				{server.tools?.length === 1 ? 'tool' : 'tools'}
			</span>
			<span class="text-muted-foreground text-xs">
				<RelativeTime value={server.toolsCachedAt} />
			</span>
		</span>
	{:else}
		<span class="text-muted-foreground">Not tested</span>
	{/if}
{/snippet}

{#snippet enabledCell(server: McpServer)}
	<Switch
		checked={server.enabled}
		disabled={toggling[server.id]}
		aria-label="{server.enabled ? 'Disable' : 'Enable'} {server.name}"
		onCheckedChange={(checked) => setEnabled(server, checked)}
	/>
{/snippet}

{#snippet actionsCell(server: McpServer)}
	<div class="flex justify-end gap-1">
		{#if server.auth.status === 'not_logged_in'}
			<Button variant="outline" size="sm" onclick={() => logIn(server)}>
				<LogInIcon data-icon="inline-start" />
				Log in
			</Button>
		{:else}
			<Button variant="outline" size="sm" onclick={() => openTest(server)}>
				<PlugZapIcon data-icon="inline-start" />
				Test
			</Button>
		{/if}
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon-sm"
						aria-label="More actions for {server.name}"
					>
						<EllipsisIcon />
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end">
				<DropdownMenu.Item onSelect={() => openEdit(server)}>
					<PencilIcon />
					Edit
				</DropdownMenu.Item>
				{#if server.auth.status === 'not_logged_in'}
					<DropdownMenu.Item onSelect={() => openTest(server)}>
						<PlugZapIcon />
						Test
					</DropdownMenu.Item>
				{:else if canLogIn(server.auth)}
					<DropdownMenu.Item onSelect={() => logIn(server)}>
						<LogInIcon />
						{server.auth.status === 'oauth' ? 'Log in again' : 'Log in'}
					</DropdownMenu.Item>
				{/if}
				{#if server.auth.loggedInAt}
					<DropdownMenu.Item onSelect={() => confirmLogout(server)}>
						<LogOutIcon />
						Log out
					</DropdownMenu.Item>
				{/if}
				<DropdownMenu.Item variant="destructive" onSelect={() => confirmDelete(server)}>
					<Trash2Icon />
					Delete
				</DropdownMenu.Item>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

<svelte:head>
	<title>MCP Servers · Umpteenth</title>
</svelte:head>

<PageHeader
	title="MCP Servers"
	description="Tools your jobs can use through the Model Context Protocol."
>
	{#snippet actions()}
		<Button onclick={openAdd}>
			<PlusIcon data-icon="inline-start" />
			Add server
		</Button>
	{/snippet}
</PageHeader>

<DataTable
	bind:this={dataTable}
	label="MCP servers"
	{columns}
	fetchPage={(query) => mcpService.list(query)}
	getRowId={(server) => server.id}
	defaultSort="name"
	searchPlaceholder="Search servers"
	filters={[
		{
			key: 'transport',
			label: 'Transport',
			options: [
				{ value: 'stdio', label: 'stdio' },
				{ value: 'http', label: 'HTTP' }
			]
		}
	]}
>
	{#snippet empty()}
		<Empty.Root class="py-6">
			<Empty.Header>
				<Empty.Media variant="icon">
					<ServerIcon />
				</Empty.Media>
				<Empty.Title>No MCP servers yet</Empty.Title>
				<Empty.Description>
					Register stdio or HTTP MCP servers to give jobs access to more tools.
				</Empty.Description>
			</Empty.Header>
			<Empty.Content>
				<Button onclick={openAdd}>
					<PlusIcon data-icon="inline-start" />
					Add server
				</Button>
			</Empty.Content>
		</Empty.Root>
	{/snippet}
</DataTable>

<ServerDialog bind:open={dialogOpen} server={editing} {onSaved} />
<ServerTestDialog bind:server={testing} {onTested} onLogIn={logIn} />
<ServerSheet
	bind:server={viewing}
	onTest={openTest}
	onEdit={openEdit}
	onLogIn={logIn}
	onLogOut={confirmLogout}
/>
